#!/bin/bash

# Remove AWS Lambda deployment for a dashboard
# Removes Lambda function, API Gateway, and DNS record
# Usage: AWS_PROFILE=deploy-admin ./remove-lambda.sh bedrock-usage example.com

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Check arguments
if [ $# -ne 2 ]; then
    echo "Usage: $0 <dashboard-name> <fqdn>"
    echo "Example: AWS_PROFILE=deploy-admin ./remove-lambda.sh bedrock-usage app.example.com"
    exit 1
fi

DASHBOARD_NAME="$1"
FQDN="$2"
LAMBDA_FUNCTION_NAME="${DASHBOARD_NAME}-api"
API_NAME="${DASHBOARD_NAME}-api"

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}AWS Lambda Removal for $DASHBOARD_NAME${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""

# Check AWS CLI
if ! command -v aws &> /dev/null; then
    echo -e "${RED}❌ Error: AWS CLI is not installed${NC}"
    exit 1
fi

# Check AWS credentials
echo -e "${BLUE}Checking AWS credentials...${NC}"
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text 2>/dev/null || true)

if [ -z "$ACCOUNT_ID" ]; then
    echo -e "${RED}❌ Error: Unable to authenticate with AWS${NC}"
    exit 1
fi

REGION=$(aws configure get region || echo "us-east-1")
echo -e "${GREEN}✓ Account ID: $ACCOUNT_ID${NC}"
echo -e "${GREEN}✓ Region: $REGION${NC}"
echo ""

# Confirm removal
echo -e "${YELLOW}⚠️  WARNING: This will permanently remove the Lambda deployment${NC}"
echo ""
echo "  Lambda Function: $LAMBDA_FUNCTION_NAME"
echo "  API Gateway: $API_NAME"
echo "  FQDN: $FQDN"
echo ""
read -p "Are you sure you want to continue? (type 'yes' to confirm): " confirmation

if [ "$confirmation" != "yes" ]; then
    echo -e "${YELLOW}Removal cancelled${NC}"
    exit 0
fi

echo ""

# Step 1: Find and remove DNS record
echo -e "${BLUE}Step 1: Removing Route 53 DNS record...${NC}"

# Extract domain from FQDN (last two parts for Route 53 hosted zone)
IFS='.' read -ra FQDN_PARTS <<< "$FQDN"
DOMAIN_LENGTH=${#FQDN_PARTS[@]}

if [ $DOMAIN_LENGTH -lt 2 ]; then
    echo -e "${RED}❌ Error: Invalid FQDN format: $FQDN${NC}"
    exit 1
fi

HOSTED_ZONE="${FQDN_PARTS[$((DOMAIN_LENGTH-2))]} . ${FQDN_PARTS[$((DOMAIN_LENGTH-1))]}"
HOSTED_ZONE="${HOSTED_ZONE// /}"

ZONE_ID=$(aws route53 list-hosted-zones-by-name --query "HostedZones[?Name=='${HOSTED_ZONE}.'].Id" --output text 2>/dev/null | cut -d'/' -f3)

if [ -z "$ZONE_ID" ]; then
    echo -e "${YELLOW}⚠️  Warning: Route 53 hosted zone not found: $HOSTED_ZONE${NC}"
else
    # Check if record exists
    RECORD=$(aws route53 list-resource-record-sets \
        --hosted-zone-id "$ZONE_ID" \
        --query "ResourceRecordSets[?Name=='${FQDN}.']" \
        --output json 2>/dev/null)

    if echo "$RECORD" | grep -q "$FQDN"; then
        CHANGE_BATCH="{
          \"Changes\": [{
            \"Action\": \"DELETE\",
            \"ResourceRecordSet\": $(echo "$RECORD" | python3 -c "import sys, json; records = json.load(sys.stdin); print(json.dumps(records[0])) if records else print('{}')"),
          }]
        }"

        if aws route53 change-resource-record-sets \
            --hosted-zone-id "$ZONE_ID" \
            --change-batch "$CHANGE_BATCH" 2>/dev/null; then
            echo -e "${GREEN}✓ Removed DNS record: $FQDN${NC}"
        else
            echo -e "${YELLOW}⚠️  Could not remove DNS record (may already be deleted)${NC}"
        fi
    else
        echo -e "${YELLOW}⚠️  DNS record not found: $FQDN${NC}"
    fi
fi

echo ""

# Step 2: Remove API Gateway
echo -e "${BLUE}Step 2: Removing API Gateway...${NC}"

API_ID=$(aws apigatewayv2 get-apis --query "Items[?Name=='$API_NAME'].ApiId" --output text 2>/dev/null || echo "")

if [ -z "$API_ID" ]; then
    echo -e "${YELLOW}⚠️  API Gateway not found: $API_NAME${NC}"
else
    if aws apigatewayv2 delete-api --api-id "$API_ID" 2>/dev/null; then
        echo -e "${GREEN}✓ Removed API Gateway: $API_ID${NC}"
    else
        echo -e "${YELLOW}⚠️  Could not remove API Gateway (may already be deleted)${NC}"
    fi
fi

echo ""

# Step 3: Remove Lambda function
echo -e "${BLUE}Step 3: Removing Lambda function...${NC}"

if aws lambda get-function --function-name "$LAMBDA_FUNCTION_NAME" 2>/dev/null > /dev/null; then
    if aws lambda delete-function --function-name "$LAMBDA_FUNCTION_NAME" 2>/dev/null; then
        echo -e "${GREEN}✓ Removed Lambda function: $LAMBDA_FUNCTION_NAME${NC}"
    else
        echo -e "${YELLOW}⚠️  Could not remove Lambda function${NC}"
    fi
else
    echo -e "${YELLOW}⚠️  Lambda function not found: $LAMBDA_FUNCTION_NAME${NC}"
fi

echo ""

# Step 4: Optional - Remove IAM role (only if it's not used by other Lambda functions)
echo -e "${BLUE}Step 4: Checking IAM role...${NC}"

LAMBDA_FUNCTIONS=$(aws lambda list-functions --query "Functions[?Role=='arn:aws:iam::${ACCOUNT_ID}:role/lambda-bedrock-role'].FunctionName" --output text 2>/dev/null || echo "")

if [ -z "$LAMBDA_FUNCTIONS" ]; then
    echo -e "${YELLOW}No other Lambda functions using lambda-bedrock-role${NC}"
    read -p "Remove IAM role? (y/n): " remove_role

    if [ "$remove_role" = "y" ]; then
        # Detach policies
        aws iam detach-role-policy \
            --role-name lambda-bedrock-role \
            --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole 2>/dev/null || true

        # Delete role
        if aws iam delete-role --role-name lambda-bedrock-role 2>/dev/null; then
            echo -e "${GREEN}✓ Removed IAM role: lambda-bedrock-role${NC}"
        else
            echo -e "${YELLOW}⚠️  Could not remove IAM role (may have other policies)${NC}"
        fi
    fi
else
    echo -e "${YELLOW}Other Lambda functions still using lambda-bedrock-role (not removed)${NC}"
fi

echo ""

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}✅ Lambda Removal Complete!${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""
