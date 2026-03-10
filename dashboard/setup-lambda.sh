#!/bin/bash

# Deploy Flask application as AWS Lambda function with API Gateway
# Supports all dashboards with their own settings
# Usage: ./setup-lambda.sh <folder-name> [--profile <profile>] [--fqdn <domain>] [--subnets-only <cidr,...>]

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse arguments
DASHBOARD_NAME=""
AWS_PROFILE="${AWS_PROFILE:-default}"
IAM_PROFILE=""
FQDN=""
SUBNETS_ONLY=""
PROFILE_PROVIDED=false
FQDN_PROVIDED=false
SUBNETS_ONLY_PROVIDED=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --help|-h)
            echo "Usage: $0 <folder-name> [--profile <profile>] [--iam-profile <profile>] [--fqdn <domain>] [--subnets-only <cidr,...>]"
            echo ""
            echo "Options:"
            echo "  --profile <profile>          AWS CLI profile to use (default: AWS_PROFILE env var or 'default')"
            echo "                               Requires Lambda, API Gateway, and IAM permissions"
            echo "  --iam-profile <profile>      IAM admin profile for granting permissions when --profile lacks IAM access"
            echo "                               Used only if --profile fails due to insufficient permissions"
            echo "  --fqdn <domain>              Fully qualified domain name for Route 53 (if omitted, uses API Gateway endpoint only)"
            echo "  --subnets-only <cidr,...>    Comma-separated CIDR blocks for access control (e.g., 192.168.0.0/16,10.0.0.0/8)"
            echo "  --help                       Show this help message"
            echo ""
            echo "Examples:"
            echo "  ./setup-lambda.sh <folder> --profile prod"
            echo "  ./setup-lambda.sh <folder> --profile prod --fqdn bedrock.example.com"
            echo "  ./setup-lambda.sh <folder> --profile prod --fqdn bedrock.example.com --subnets-only 192.168.0.0/16"
            echo "  ./setup-lambda.sh <folder> --profile prod --iam-profile admin --subnets-only 192.168.0.0/16"
            echo ""
            echo "Environment Variables (can be overridden by command-line args):"
            echo "  AWS_PROFILE         AWS CLI profile (override with --profile)"
            echo "  IAM_PROFILE         IAM admin profile (override with --iam-profile)"
            exit 0
            ;;
        --profile)
            AWS_PROFILE="$2"
            PROFILE_PROVIDED=true
            shift 2
            ;;
        --iam-profile)
            IAM_PROFILE="$2"
            shift 2
            ;;
        --fqdn)
            FQDN="$2"
            FQDN_PROVIDED=true
            shift 2
            ;;
        --subnets-only)
            SUBNETS_ONLY="$2"
            SUBNETS_ONLY_PROVIDED=true
            shift 2
            ;;
        *)
            if [ -z "$DASHBOARD_NAME" ]; then
                DASHBOARD_NAME="$1"
            else
                echo "Error: Unknown argument: $1"
                exit 1
            fi
            shift
            ;;
    esac
done

# Check if dashboard name was provided
if [ -z "$DASHBOARD_NAME" ]; then
    echo "Usage: $0 <folder-name> [--profile <profile>] [--iam-profile <profile>] [--fqdn <domain>] [--subnets-only <cidr,...>]"
    echo ""
    echo "Options:"
    echo "  --profile <profile>          AWS CLI profile to use (default: AWS_PROFILE env var or 'default')"
    echo "                               Requires Lambda, API Gateway, and IAM permissions"
    echo "  --iam-profile <profile>      IAM admin profile for granting permissions when --profile lacks IAM access"
    echo "                               Used only if --profile fails due to insufficient permissions"
    echo "  --fqdn <domain>              Fully qualified domain name for Route 53 (if omitted, uses API Gateway endpoint only)"
    echo "  --subnets-only <cidr,...>    Comma-separated CIDR blocks for access control (e.g., 192.168.0.0/16,10.0.0.0/8)"
    echo ""
    echo "Examples:"
    echo "  ./setup-lambda.sh <folder> --profile prod"
    echo "  ./setup-lambda.sh <folder> --profile prod --fqdn bedrock.example.com"
    echo "  ./setup-lambda.sh <folder> --profile prod --fqdn bedrock.example.com --subnets-only 192.168.0.0/16"
    echo "  ./setup-lambda.sh <folder> --profile prod --iam-profile admin --subnets-only 192.168.0.0/16"
    echo ""
    echo "Environment Variables (can be overridden by command-line args):"
    echo "  AWS_PROFILE         AWS CLI profile (override with --profile)"
    echo "  IAM_PROFILE         IAM admin profile (override with --iam-profile)"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DASHBOARD_DIR="$SCRIPT_DIR/$DASHBOARD_NAME"
LAMBDA_FUNCTION_NAME="${DASHBOARD_NAME}-api"

# Load previous deployment metadata for settings preservation
# This must happen before any configuration output
METADATA_FILE="$HOME/.lambda-deployments/$LAMBDA_FUNCTION_NAME.metadata"
if [ -f "$METADATA_FILE" ]; then
    # Source the metadata file to get previous deployment settings
    source "$METADATA_FILE" 2>/dev/null || true
    # Variables SUBNETS_ONLY and FQDN are now set (may be empty)
fi

# Determine final configuration values with proper precedence:
# 1. Command-line arguments (if provided)
# 2. Previous deployment settings (if not provided on command line)
# 3. Empty (if neither provided)

# Set final subnet restrictions
if [ "$SUBNETS_ONLY_PROVIDED" = true ]; then
    # Command line explicitly provided (could be empty to disable)
    FINAL_SUBNETS_ONLY="$SUBNETS_ONLY"
elif [ -n "$SUBNETS_ONLY" ]; then
    # Loaded from metadata file (previous deployment)
    FINAL_SUBNETS_ONLY="$SUBNETS_ONLY"
else
    # No previous setting and not provided on command line
    FINAL_SUBNETS_ONLY=""
fi

# Set final FQDN
if [ "$FQDN_PROVIDED" = true ]; then
    # Command line explicitly provided (could be empty)
    FINAL_FQDN="$FQDN"
elif [ -n "$FQDN" ]; then
    # Loaded from metadata file (previous deployment)
    FINAL_FQDN="$FQDN"
else
    # No previous setting and not provided on command line
    FINAL_FQDN=""
fi

# Validate dashboard directory exists
if [ ! -d "$DASHBOARD_DIR" ]; then
    echo "❌ Error: Dashboard directory '$DASHBOARD_DIR' not found"
    exit 1
fi

if [ ! -f "$DASHBOARD_DIR/app.py" ]; then
    echo "❌ Error: app.py not found in $DASHBOARD_DIR"
    exit 1
fi

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}AWS Lambda Deployment for $DASHBOARD_NAME${NC}"
if [ -z "$FINAL_FQDN" ]; then
    echo -e "${BLUE}(No Route 53 DNS - API Gateway endpoint only)${NC}"
fi
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""

# Check AWS CLI
if ! command -v aws &> /dev/null; then
    echo -e "${RED}❌ Error: AWS CLI is not installed${NC}"
    exit 1
fi

# Check AWS credentials
echo -e "${BLUE}Checking AWS credentials...${NC}"
ACCOUNT_ID=$(aws sts get-caller-identity --profile "$AWS_PROFILE" --query Account --output text 2>/dev/null || true)

if [ -z "$ACCOUNT_ID" ]; then
    echo -e "${RED}❌ Error: Unable to authenticate with AWS${NC}"
    echo "Make sure --profile or AWS_PROFILE is set correctly"
    exit 1
fi

REGION=$(aws configure get region --profile "$AWS_PROFILE" || echo "us-east-1")
echo -e "${GREEN}✓ Profile: $AWS_PROFILE${NC}"
echo -e "${GREEN}✓ Account ID: $ACCOUNT_ID${NC}"
echo -e "${GREEN}✓ Region: $REGION${NC}"
if [ -n "$IAM_PROFILE" ]; then
    echo -e "${GREEN}✓ IAM Profile: $IAM_PROFILE (for permission grants)${NC}"
fi
if [ -n "$FQDN" ]; then
    echo -e "${GREEN}✓ FQDN: $FQDN${NC}"
fi
if [ -n "$SUBNETS_ONLY" ]; then
    echo -e "${GREEN}✓ Subnets: $SUBNETS_ONLY${NC}"
fi
echo ""

# Function to prompt for input with default value
prompt_input() {
    local prompt="$1"
    local default="$2"
    local var_name="$3"
    local input_value=""

    if [ -z "$default" ]; then
        read -p "  $prompt: " input_value
    else
        read -p "  $prompt [$default]: " input_value
        input_value="${input_value:-$default}"
    fi

    eval "$var_name='$input_value'"
}

# Function to get app-specific variable name
get_app_var() {
    local var_name="$1"
    echo "${var_name}_${DASHBOARD_NAME^^}"
}

# Configuration settings
# Use command-line arguments if provided, otherwise prompt
echo -e "${YELLOW}════════════════════════════════════════════════════════════${NC}"
echo -e "${YELLOW}CONFIGURATION SETTINGS${NC}"
echo -e "${YELLOW}════════════════════════════════════════════════════════════${NC}"
echo ""

# If command-line args were provided, use them directly
if [ "$FQDN_PROVIDED" = true ] || [ "$SUBNETS_ONLY_PROVIDED" = true ] || [ "$PROFILE_PROVIDED" = true ]; then
    # Command-line arguments were provided, skip interactive prompts
    echo -e "${GREEN}Using command-line arguments (non-interactive mode)${NC}"
    echo ""

    FINAL_AWS_PROFILE="$AWS_PROFILE"
    # Note: FINAL_SUBNETS_ONLY and FINAL_FQDN are already set by the preservation logic above
    # Just use them as-is for non-interactive mode
else
    # Interactive mode: prompt for configuration
    echo -e "${BLUE}Global Settings (apply to all dashboards):${NC}"
    prompt_input "AWS Profile" "$AWS_PROFILE" "AWS_PROFILE"
    prompt_input "Subnets restriction (comma-separated CIDRs, leave empty for no restriction)" "" "SUBNETS_ONLY"
    prompt_input "FQDN (fully qualified domain name, leave empty to use API Gateway endpoint only)" "" "FQDN"
    echo ""

    APP_AWS_VAR=$(get_app_var "AWS_PROFILE")
    APP_SUBNET_VAR=$(get_app_var "SUBNETS_ONLY")
    APP_FQDN_VAR=$(get_app_var "FQDN")

    echo -e "${BLUE}Dashboard-Specific Settings (override global):${NC}"
    echo "(Leave blank to use global settings)"
    prompt_input "AWS Profile (app-specific)" "" "APP_AWS_PROFILE"
    prompt_input "Subnets restriction (app-specific)" "" "APP_SUBNETS_ONLY"
    prompt_input "FQDN (app-specific, leave empty to use API Gateway endpoint only)" "" "APP_FQDN"
    echo ""

    # Use global or app-specific values
    FINAL_AWS_PROFILE="${APP_AWS_PROFILE:-$AWS_PROFILE}"
    FINAL_SUBNETS_ONLY="${APP_SUBNETS_ONLY:-$SUBNETS_ONLY}"
    FINAL_FQDN="${APP_FQDN:-$FQDN}"
fi

echo -e "${GREEN}✓ AWS Profile: $FINAL_AWS_PROFILE${NC}"
echo -e "${GREEN}✓ Subnets Only: ${FINAL_SUBNETS_ONLY:-none}${NC}"
if [ -n "$FINAL_FQDN" ]; then
    echo -e "${GREEN}✓ FQDN: $FINAL_FQDN${NC}"
fi
echo ""

# Find matching Route 53 hosted zone for FQDN
# Only if DNS is enabled
if [ -n "$FINAL_FQDN" ]; then
    # Try to find the exact hosted zone that matches this FQDN
    # Start with full domain, then try parent domains
    CURRENT_DOMAIN="$FINAL_FQDN"
    HOSTED_ZONE=""

    while [ -n "$CURRENT_DOMAIN" ]; do
        # Query Route 53 for this domain
        ZONE_CHECK=$(aws route53 list-hosted-zones-by-name \
            --profile "$FINAL_AWS_PROFILE" \
            --query "HostedZones[?Name=='${CURRENT_DOMAIN}.'].Name" \
            --output text 2>/dev/null || echo "")

        if [ -n "$ZONE_CHECK" ]; then
            HOSTED_ZONE="$CURRENT_DOMAIN"
            break
        fi

        # Try parent domain (remove first part)
        CURRENT_DOMAIN="${CURRENT_DOMAIN#*.}"

        # Prevent infinite loop
        if [ "$CURRENT_DOMAIN" = "$PREVIOUS_DOMAIN" ]; then
            break
        fi
        PREVIOUS_DOMAIN="$CURRENT_DOMAIN"
    done

    if [ -z "$HOSTED_ZONE" ]; then
        echo -e "${RED}❌ Error: Could not find Route 53 hosted zone for: $FINAL_FQDN${NC}"
        echo "Available zones: "
        aws route53 list-hosted-zones --profile "$FINAL_AWS_PROFILE" --query "HostedZones[].Name" --output text
        exit 1
    fi
fi

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}LAMBDA DEPLOYMENT${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""

# Step 1: Check if Route 53 hosted zone exists (skip if --no-dns)
if [ -n "$FINAL_FQDN" ]; then
    echo -e "${BLUE}Step 1: Checking Route 53 hosted zone...${NC}"
    ZONE_ID=$(aws route53 list-hosted-zones-by-name --profile "$FINAL_AWS_PROFILE" --query "HostedZones[?Name=='${HOSTED_ZONE}.'].Id" --output text 2>/dev/null | cut -d'/' -f3)

    if [ -z "$ZONE_ID" ]; then
        echo -e "${RED}❌ Error: Route 53 hosted zone not found for: $HOSTED_ZONE${NC}"
        echo "Please create a hosted zone in Route 53 first"
        exit 1
    fi

    echo -e "${GREEN}✓ Found hosted zone: $HOSTED_ZONE (ID: $ZONE_ID)${NC}"
    echo ""
fi

# Step 2: Create deployment package
echo -e "${BLUE}Step 2: Creating deployment package...${NC}"
LAMBDA_FUNCTION_NAME="${DASHBOARD_NAME}-api"
BUILD_DIR="/tmp/${LAMBDA_FUNCTION_NAME}-build"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Copy application files
cp "$DASHBOARD_DIR/app.py" "$BUILD_DIR/"
cp "$DASHBOARD_DIR"/*.html "$BUILD_DIR/" 2>/dev/null || true

# Create requirements file in build dir
cp "$SCRIPT_DIR/requirements.txt" "$BUILD_DIR/"

# Create a minimal .env file for Lambda (will be loaded by app.py)
cat > "$BUILD_DIR/.env" << ENVEOF
# Lambda deployment - environment variables set by lambda_handler.py
ENVEOF

# Create config file with deployment settings
# Note: AWS_PROFILE is NOT used in Lambda - Lambda uses IAM roles instead
# Note: FINAL_SUBNETS_ONLY and FINAL_FQDN are already determined by preservation logic

cat > "$BUILD_DIR/config.json" << EOF
{
  "subnets_only": "$FINAL_SUBNETS_ONLY",
  "fqdn": "$FINAL_FQDN"
}
EOF

# Create Lambda handler wrapper
cat > "$BUILD_DIR/lambda_handler.py" << 'HANDLER_EOF'
import json
import sys
import os
from pathlib import Path

# Load config
config_file = Path(__file__).parent / 'config.json'
config = json.load(open(config_file))

# Set environment variables
# Note: Lambda uses IAM role for AWS credentials, NOT AWS_PROFILE
if config.get('subnets_only'):
    os.environ['SUBNETS_ONLY'] = config['subnets_only']
if config.get('fqdn'):
    os.environ['FQDN'] = config['fqdn']

# Import app
from app import app

def lambda_handler(event, context):
    """Lambda entry point for Flask application"""

    # Handle API Gateway v2 format
    if event.get('version') == '2.0':
        method = event['requestContext']['http']['method']
        path = event['rawPath']
        body = event.get('body', '')
        headers = event.get('headers', {})
    else:
        # API Gateway v1 format
        method = event['httpMethod']
        path = event['path']
        body = event.get('body', '')
        headers = event.get('headers', {})

    # Create WSGI environ
    environ = {
        'REQUEST_METHOD': method,
        'SCRIPT_NAME': '',
        'PATH_INFO': path,
        'QUERY_STRING': event.get('rawQueryString', ''),
        'CONTENT_TYPE': headers.get('content-type', ''),
        'CONTENT_LENGTH': str(len(body)) if body else '',
        'SERVER_NAME': headers.get('host', 'lambda.amazonaws.com').split(':')[0],
        'SERVER_PORT': headers.get('x-forwarded-port', '443'),
        'SERVER_PROTOCOL': 'HTTP/1.1',
        'wsgi.version': (1, 0),
        'wsgi.url_scheme': headers.get('x-forwarded-proto', 'https'),
        'wsgi.input': None,
        'wsgi.errors': sys.stderr,
        'wsgi.multithread': True,
        'wsgi.multiprocess': False,
        'wsgi.run_once': False,
    }

    # Add headers to environ
    for header, value in headers.items():
        header_key = 'HTTP_' + header.upper().replace('-', '_')
        environ[header_key] = value

    # Call Flask app
    response_data = []
    status = None
    response_headers = None

    def start_response(status_str, headers_list):
        nonlocal status, response_headers
        status = int(status_str.split()[0])
        response_headers = dict(headers_list)

    response_data = app.wsgi_app(environ, start_response)

    # Combine response
    body_str = ''.join([chunk.decode('utf-8') if isinstance(chunk, bytes) else chunk for chunk in response_data])

    return {
        'statusCode': status or 200,
        'headers': response_headers or {},
        'body': body_str
    }
HANDLER_EOF

echo -e "${GREEN}✓ Created deployment package${NC}"
echo ""

# Step 3: Create Lambda function (or update if exists)
echo -e "${BLUE}Step 3: Creating/updating Lambda function...${NC}"

# Install dependencies
pip_output=$(.venv/bin/pip install -q -r "$BUILD_DIR/requirements.txt" -t "$BUILD_DIR/" 2>&1 || true)

# Create zip file
cd "$BUILD_DIR"
zip -r -q "${LAMBDA_FUNCTION_NAME}.zip" . -x "*.git*" "*.venv*" "*__pycache__*" "*.pyc"
cd - > /dev/null

LAMBDA_ROLE_NAME="lambda-${DASHBOARD_NAME}-role"
LAMBDA_ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${LAMBDA_ROLE_NAME}"

# Check if function exists
if aws lambda get-function --profile "$FINAL_AWS_PROFILE" --function-name "$LAMBDA_FUNCTION_NAME" 2>/dev/null; then
    echo -e "${YELLOW}Updating existing Lambda function...${NC}"
    aws lambda update-function-code --profile "$FINAL_AWS_PROFILE" \
        --function-name "$LAMBDA_FUNCTION_NAME" \
        --zip-file "fileb://${BUILD_DIR}/${LAMBDA_FUNCTION_NAME}.zip" > /dev/null
else
    echo -e "${YELLOW}Creating new Lambda function...${NC}"

    # Determine which profile to use for IAM operations
    IAM_OPS_PROFILE="$FINAL_AWS_PROFILE"
    if [ -n "$IAM_PROFILE" ]; then
        # Test if FINAL_AWS_PROFILE has IAM permissions, fallback to IAM_PROFILE if not
        TRUST_POLICY='{
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Principal": {
                "Service": "lambda.amazonaws.com"
              },
              "Action": "sts:AssumeRole"
            }
          ]
        }'
        if ! aws iam create-role --profile "$FINAL_AWS_PROFILE" --dry-run \
            --role-name "test-role-$$" \
            --assume-role-policy-document "$TRUST_POLICY" 2>/dev/null; then
            echo -e "${YELLOW}⚠️  $FINAL_AWS_PROFILE lacks IAM permissions, using $IAM_PROFILE for IAM operations${NC}"
            IAM_OPS_PROFILE="$IAM_PROFILE"
        fi
    fi

    # Check if role exists (try both profiles if applicable)
    ROLE_EXISTS=false
    if aws iam get-role --profile "$FINAL_AWS_PROFILE" --role-name "$LAMBDA_ROLE_NAME" 2>/dev/null; then
        ROLE_EXISTS=true
    elif [ -n "$IAM_PROFILE" ] && aws iam get-role --profile "$IAM_PROFILE" --role-name "$LAMBDA_ROLE_NAME" 2>/dev/null; then
        ROLE_EXISTS=true
    fi

    if [ "$ROLE_EXISTS" = false ]; then
        echo -e "${YELLOW}Creating IAM role for Lambda...${NC}"

        TRUST_POLICY='{
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Principal": {
                "Service": "lambda.amazonaws.com"
              },
              "Action": "sts:AssumeRole"
            }
          ]
        }'

        aws iam create-role --profile "$IAM_OPS_PROFILE" \
            --role-name "$LAMBDA_ROLE_NAME" \
            --assume-role-policy-document "$TRUST_POLICY" > /dev/null

        # Attach policy for CloudWatch logs
        aws iam attach-role-policy --profile "$IAM_OPS_PROFILE" \
            --role-name "$LAMBDA_ROLE_NAME" \
            --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole > /dev/null

        # Create and attach policy for Bedrock logs access
        BEDROCK_POLICY_NAME="lambda-bedrock-logs-access"
        BEDROCK_POLICY_DOCUMENT='{
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Action": [
                "logs:*"
              ],
              "Resource": "arn:aws:logs:*:*:log-group:/aws/bedrock/modelinvocations*"
            },
            {
              "Effect": "Allow",
              "Action": [
                "bedrock:GetModelInvocationLoggingConfiguration"
              ],
              "Resource": "*"
            }
          ]
        }'

        aws iam put-role-policy --profile "$IAM_OPS_PROFILE" \
            --role-name "$LAMBDA_ROLE_NAME" \
            --policy-name "$BEDROCK_POLICY_NAME" \
            --policy-document "$BEDROCK_POLICY_DOCUMENT" > /dev/null

        echo -e "${GREEN}✓ Created IAM role: $LAMBDA_ROLE_NAME${NC}"
        if [ "$IAM_OPS_PROFILE" != "$FINAL_AWS_PROFILE" ]; then
            echo -e "${GREEN}✓ Granted using: $IAM_OPS_PROFILE${NC}"
        fi

        # Wait for IAM role to propagate
        echo -e "${YELLOW}Waiting for IAM role to propagate (10 seconds)...${NC}"
        sleep 10
    fi

    aws lambda create-function --profile "$FINAL_AWS_PROFILE" \
        --function-name "$LAMBDA_FUNCTION_NAME" \
        --runtime python3.11 \
        --role "$LAMBDA_ROLE_ARN" \
        --handler lambda_handler.lambda_handler \
        --timeout 30 \
        --memory-size 512 \
        --zip-file "fileb://${BUILD_DIR}/${LAMBDA_FUNCTION_NAME}.zip" > /dev/null

    # Wait for function to be created
    sleep 2
fi

# Store deployment metadata for retrieval on next deployment
# Save to a local file that will be checked for preservation logic
METADATA_FILE="$HOME/.lambda-deployments/$LAMBDA_FUNCTION_NAME.metadata"
mkdir -p "$(dirname "$METADATA_FILE")"

cat > "$METADATA_FILE" << METAEOF
# Lambda deployment metadata - auto-generated for setting preservation
SUBNETS_ONLY="$FINAL_SUBNETS_ONLY"
FQDN="$FINAL_FQDN"
METAEOF

echo -e "${GREEN}✓ Saved deployment metadata${NC}"

echo -e "${GREEN}✓ Lambda function ready: $LAMBDA_FUNCTION_NAME${NC}"
echo ""

# Step 4: Create/update API Gateway
echo -e "${BLUE}Step 4: Setting up API Gateway...${NC}"

API_NAME="${DASHBOARD_NAME}-api"

# Check if API exists
API_ID=$(aws apigatewayv2 get-apis --profile "$FINAL_AWS_PROFILE" --query "Items[?Name=='$API_NAME'].ApiId" --output text 2>/dev/null)

if [ -z "$API_ID" ]; then
    echo -e "${YELLOW}Creating new API Gateway...${NC}"

    API_ID=$(aws apigatewayv2 create-api --profile "$FINAL_AWS_PROFILE" \
        --name "$API_NAME" \
        --protocol-type HTTP \
        --target "arn:aws:lambda:${REGION}:${ACCOUNT_ID}:function:${LAMBDA_FUNCTION_NAME}" \
        --query 'ApiId' \
        --output text)
else
    echo -e "${YELLOW}API Gateway already exists: $API_ID${NC}"
fi

API_ENDPOINT="${API_ID}.execute-api.${REGION}.amazonaws.com"

# Grant API Gateway permission to invoke Lambda
echo -e "${YELLOW}Granting API Gateway invoke permission to Lambda...${NC}"
aws lambda add-permission --profile "$FINAL_AWS_PROFILE" \
    --function-name "$LAMBDA_FUNCTION_NAME" \
    --statement-id AllowAPIGatewayInvoke \
    --action lambda:InvokeFunction \
    --principal apigateway.amazonaws.com \
    --source-arn "arn:aws:execute-api:${REGION}:${ACCOUNT_ID}:${API_ID}/*/*" 2>/dev/null || echo "Permission already exists"

echo -e "${GREEN}✓ API Gateway: $API_ID${NC}"
echo -e "${GREEN}✓ API Endpoint: https://${API_ENDPOINT}${NC}"
echo ""

# Step 5: Setup SSL Certificate and Custom Domain (if FQDN provided)
if [ -n "$FINAL_FQDN" ]; then
    echo -e "${BLUE}Step 5: Setting up SSL certificate and custom domain...${NC}"

    # Step 5a: Create ACM Certificate
    echo -e "${YELLOW}Creating ACM certificate for $FINAL_FQDN...${NC}"

    CERT_ARN=$(aws acm request-certificate \
        --domain-name "$FINAL_FQDN" \
        --validation-method DNS \
        --region "$REGION" \
        --profile "$FINAL_AWS_PROFILE" \
        --query 'CertificateArn' \
        --output text)

    if [ -z "$CERT_ARN" ]; then
        echo -e "${RED}❌ Error: Failed to create ACM certificate${NC}"
        exit 1
    fi

    echo -e "${GREEN}✓ Created ACM certificate: $CERT_ARN${NC}"
    echo -e "${YELLOW}Waiting for DNS validation records to be available...${NC}"

    # Wait for certificate to have validation records
    sleep 10
    for i in {1..60}; do
        VALIDATION_RECORDS=$(aws acm describe-certificate \
            --certificate-arn "$CERT_ARN" \
            --region "$REGION" \
            --profile "$FINAL_AWS_PROFILE" \
            --query 'Certificate.DomainValidationOptions[0].ResourceRecord' \
            --output json 2>/dev/null || echo "{}")

        if [ "$VALIDATION_RECORDS" != "{}" ] && [ "$VALIDATION_RECORDS" != "null" ]; then
            break
        fi
        echo -e "${YELLOW}Waiting... (attempt $i/60)${NC}"
        sleep 3
    done

    # Use jq to safely extract JSON fields if available, fallback to grep
    if command -v jq &> /dev/null; then
        VALIDATION_NAME=$(echo "$VALIDATION_RECORDS" | jq -r '.Name // empty')
        VALIDATION_VALUE=$(echo "$VALIDATION_RECORDS" | jq -r '.Value // empty')
    else
        VALIDATION_NAME=$(echo "$VALIDATION_RECORDS" | grep -o '"Name": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)".*/\1/')
        VALIDATION_VALUE=$(echo "$VALIDATION_RECORDS" | grep -o '"Value": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)".*/\1/')
    fi

    if [ -z "$VALIDATION_NAME" ] || [ -z "$VALIDATION_VALUE" ]; then
        echo -e "${RED}❌ Error: Could not retrieve DNS validation records${NC}"
        echo "Certificate ARN: $CERT_ARN"
        echo "You may need to validate manually in the AWS console"
        exit 1
    fi

    echo -e "${GREEN}✓ DNS validation records ready${NC}"
    echo ""

    # Step 5b: Create DNS validation record in Route 53
    echo -e "${YELLOW}Creating DNS validation record in Route 53...${NC}"

    aws route53 change-resource-record-sets \
        --hosted-zone-id "$ZONE_ID" \
        --profile "$FINAL_AWS_PROFILE" \
        --change-batch "{
            \"Changes\": [{
                \"Action\": \"CREATE\",
                \"ResourceRecordSet\": {
                    \"Name\": \"$VALIDATION_NAME\",
                    \"Type\": \"CNAME\",
                    \"TTL\": 300,
                    \"ResourceRecords\": [{\"Value\": \"$VALIDATION_VALUE\"}]
                }
            }]
        }" > /dev/null 2>&1 || echo "Validation record may already exist"

    echo -e "${GREEN}✓ Created DNS validation record${NC}"
    echo -e "${YELLOW}Waiting for certificate validation (this can take 5-10 minutes)...${NC}"
    echo ""

    # Wait for certificate validation
    VALIDATED=false
    for i in {1..60}; do
        CERT_STATUS=$(aws acm describe-certificate \
            --certificate-arn "$CERT_ARN" \
            --region "$REGION" \
            --profile "$FINAL_AWS_PROFILE" \
            --query 'Certificate.Status' \
            --output text)

        if [ "$CERT_STATUS" == "ISSUED" ]; then
            VALIDATED=true
            break
        fi
        echo -e "${YELLOW}Checking certificate status... (attempt $i/60 - Status: $CERT_STATUS)${NC}"
        sleep 10
    done

    if [ "$VALIDATED" = false ]; then
        echo -e "${RED}❌ Error: Certificate validation timeout${NC}"
        echo "Certificate ARN: $CERT_ARN"
        echo "Check the ACM console to verify validation"
        exit 1
    fi

    echo -e "${GREEN}✓ Certificate validated and issued${NC}"
    echo ""

    # Step 5c: Create custom domain in API Gateway
    echo -e "${YELLOW}Creating custom domain in API Gateway...${NC}"

    CUSTOM_DOMAIN=$(aws apigatewayv2 create-domain-name \
        --domain-name "$FINAL_FQDN" \
        --domain-name-configurations CertificateArn="$CERT_ARN",EndpointType=REGIONAL \
        --region "$REGION" \
        --profile "$FINAL_AWS_PROFILE" \
        --query 'DomainNameConfigurations[0].TargetDomainName' \
        --output text 2>/dev/null || echo "")

    if [ -z "$CUSTOM_DOMAIN" ]; then
        # Domain might already exist, try to describe it
        CUSTOM_DOMAIN=$(aws apigatewayv2 get-domain-names \
            --region "$REGION" \
            --profile "$FINAL_AWS_PROFILE" \
            --query "Items[?Name=='$FINAL_FQDN'].DomainNameConfigurations[0].TargetDomainName" \
            --output text 2>/dev/null || echo "")

        if [ -z "$CUSTOM_DOMAIN" ]; then
            echo -e "${RED}❌ Error: Failed to create custom domain${NC}"
            exit 1
        fi
        echo -e "${GREEN}✓ Custom domain already exists${NC}"
    else
        echo -e "${GREEN}✓ Created custom domain${NC}"
    fi

    echo -e "${GREEN}✓ Target domain: $CUSTOM_DOMAIN${NC}"
    echo ""

    # Step 5d: Create API mapping
    echo -e "${YELLOW}Creating API mapping...${NC}"

    # Check if mapping already exists
    EXISTING_MAPPING=$(aws apigatewayv2 get-api-mappings \
        --domain-name "$FINAL_FQDN" \
        --region "$REGION" \
        --profile "$FINAL_AWS_PROFILE" \
        --query "Items[0].ApiId" \
        --output text 2>/dev/null || echo "")

    if [ -z "$EXISTING_MAPPING" ] || [ "$EXISTING_MAPPING" == "None" ]; then
        # Create mapping
        aws apigatewayv2 create-api-mapping \
            --domain-name "$FINAL_FQDN" \
            --api-id "$API_ID" \
            --stage "\$default" \
            --region "$REGION" \
            --profile "$FINAL_AWS_PROFILE" > /dev/null

        echo -e "${GREEN}✓ Created API mapping${NC}"
    else
        echo -e "${GREEN}✓ API mapping already exists${NC}"
    fi

    echo ""

    # Step 5e: Update Route 53 record to point to custom domain
    echo -e "${YELLOW}Updating Route 53 record to point to custom domain...${NC}"

    # Extract API Gateway hosted zone ID for the region
    declare -A APIGW_ZONES=(
        [us-east-1]="Z1D633PJN98FT9"
        [us-east-2]="Z2FDTNDATAQYW2"
        [us-west-1]="Z2MUQ32089INYE"
        [us-west-2]="Z1H1FL5HABSF5"
        [eu-west-1]="ZLY8HYME6SFDD"
        [eu-central-1]="ZKCCQXN69G81H"
        [ap-southeast-1]="ZL327KTPW47FFT"
        [ap-southeast-2]="Z2W01FF0C6A6B1"
        [ap-northeast-1]="Z1YSHQZHG15Z27"
    )

    APIGW_ZONE_ID=${APIGW_ZONES[$REGION]}
    if [ -z "$APIGW_ZONE_ID" ]; then
        echo -e "${RED}❌ Error: Unknown region: $REGION${NC}"
        echo "Supported regions: ${!APIGW_ZONES[@]}"
        exit 1
    fi

    aws route53 change-resource-record-sets \
        --hosted-zone-id "$ZONE_ID" \
        --profile "$FINAL_AWS_PROFILE" \
        --change-batch "{
            \"Changes\": [{
                \"Action\": \"UPSERT\",
                \"ResourceRecordSet\": {
                    \"Name\": \"$FINAL_FQDN\",
                    \"Type\": \"A\",
                    \"AliasTarget\": {
                        \"HostedZoneId\": \"$APIGW_ZONE_ID\",
                        \"DNSName\": \"$CUSTOM_DOMAIN\",
                        \"EvaluateTargetHealth\": false
                    }
                }
            }]
        }" > /dev/null

    echo -e "${GREEN}✓ Updated Route 53 record with alias to custom domain${NC}"
    echo ""
fi

# Cleanup
rm -rf "$BUILD_DIR"

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}✅ Lambda Deployment Complete!${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""
echo -e "${GREEN}Deployment Details:${NC}"
echo "  Application: $DASHBOARD_NAME"
echo "  Lambda Function: $LAMBDA_FUNCTION_NAME"
echo "  API Gateway: $API_ID"
echo "  Region: $REGION"
if [ -n "$FINAL_FQDN" ]; then
    echo "  ACM Certificate: $CERT_ARN"
    echo "  API Endpoint: https://${API_ENDPOINT}"
    echo "  Custom Domain: https://${FINAL_FQDN}"
    echo "  Custom Domain Target: $CUSTOM_DOMAIN"
    echo ""
    echo -e "${YELLOW}⏳ DNS Propagation${NC}"
    echo "  DNS changes may take 5-15 minutes to propagate globally"
    echo "  You can check status with:"
    echo "    nslookup $FINAL_FQDN"
    echo "    dig $FINAL_FQDN"
    echo ""
    echo -e "${YELLOW}🧪 Testing${NC}"
    echo "  Once DNS propagates, test with:"
    echo "    curl -I https://$FINAL_FQDN/"
    echo "    Or open https://$FINAL_FQDN in your browser"
else
    echo "  Public Endpoint: https://${API_ENDPOINT}"
    echo ""
    echo -e "${YELLOW}Your dashboard is now publicly accessible at:${NC}"
    echo -e "${BLUE}https://${API_ENDPOINT}${NC}"
fi
echo ""
