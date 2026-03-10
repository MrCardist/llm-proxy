#!/bin/bash

# Setup cross-account DNS with SSL certificate for API Gateway
# Creates ACM certificate, API Gateway custom domain, and Route 53 mapping
# Usage: ./setup-cross-account-dns.sh --current-profile <profile> --domain-profile <profile> --domain <domain> --api-endpoint <endpoint>

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse command-line arguments
API_GATEWAY_PROFILE=""
DOMAIN_PROFILE=""
FQDN=""
API_ENDPOINT=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --api-gateway-profile)
            API_GATEWAY_PROFILE="$2"
            shift 2
            ;;
        --domain-profile)
            DOMAIN_PROFILE="$2"
            shift 2
            ;;
        --fqdn)
            FQDN="$2"
            shift 2
            ;;
        --api-endpoint)
            API_ENDPOINT="$2"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1"
            echo ""
            echo "Usage: $0 --api-gateway-profile <profile> --fqdn <domain> --api-endpoint <endpoint> [--domain-profile <profile>]"
            echo ""
            echo "Options:"
            echo "  --api-gateway-profile <profile>  AWS profile where API Gateway is deployed"
            echo "  --fqdn <domain>                  Fully qualified domain name (e.g., api.example.com)"
            echo "  --api-endpoint <endpoint>        API Gateway endpoint (e.g., g1sy8uwe75.execute-api.us-west-2.amazonaws.com)"
            echo "  --domain-profile <profile>       AWS profile where Route 53 is (default: same as --api-gateway-profile)"
            echo ""
            echo "Examples:"
            echo "  ./setup-cross-account-dns.sh --api-gateway-profile prod --fqdn api.example.com --api-endpoint g1sy8uwe75.execute-api.us-west-2.amazonaws.com"
            echo ""
            echo "  Cross-account Route 53:"
            echo "  ./setup-cross-account-dns.sh --api-gateway-profile prod --domain-profile dns-account --fqdn api.example.com --api-endpoint g1sy8uwe75.execute-api.us-west-2.amazonaws.com"
            exit 1
            ;;
    esac
done

# Validate required arguments
if [ -z "$API_GATEWAY_PROFILE" ] || [ -z "$FQDN" ] || [ -z "$API_ENDPOINT" ]; then
    echo -e "${RED}❌ Error: --api-gateway-profile, --fqdn, and --api-endpoint are required${NC}"
    echo "Usage: $0 --api-gateway-profile <profile> --fqdn <domain> --api-endpoint <endpoint> [--domain-profile <profile>]"
    exit 1
fi

# If domain profile not specified, use API gateway profile (same account)
if [ -z "$DOMAIN_PROFILE" ]; then
    DOMAIN_PROFILE="$API_GATEWAY_PROFILE"
fi

# Extract region from API endpoint (e.g., us-west-2)
API_REGION=$(echo "$API_ENDPOINT" | sed 's/.*\.execute-api\.\([^.]*\)\.amazonaws\.com.*/\1/')
if [ "$API_REGION" == "$API_ENDPOINT" ]; then
    echo -e "${RED}❌ Error: Could not extract region from API endpoint${NC}"
    exit 1
fi

# API Gateway hosted zone IDs by region
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

APIGW_ZONE_ID=${APIGW_ZONES[$API_REGION]}
if [ -z "$APIGW_ZONE_ID" ]; then
    echo -e "${RED}❌ Error: Unknown region: $API_REGION${NC}"
    echo "Supported regions: ${!APIGW_ZONES[@]}"
    exit 1
fi

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}Cross-Account DNS Setup with SSL Certificate${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""

# Check AWS CLI
if ! command -v aws &> /dev/null; then
    echo -e "${RED}❌ Error: AWS CLI is not installed${NC}"
    exit 1
fi

# Validate credentials
echo -e "${BLUE}Verifying AWS credentials...${NC}"
APIGW_ACCOUNT=$(aws sts get-caller-identity --profile "$API_GATEWAY_PROFILE" --query Account --output text 2>/dev/null || true)
DOMAIN_ACCOUNT=$(aws sts get-caller-identity --profile "$DOMAIN_PROFILE" --query Account --output text 2>/dev/null || true)

if [ -z "$APIGW_ACCOUNT" ]; then
    echo -e "${RED}❌ Error: Unable to authenticate with --api-gateway-profile ($API_GATEWAY_PROFILE)${NC}"
    exit 1
fi

if [ -z "$DOMAIN_ACCOUNT" ]; then
    echo -e "${RED}❌ Error: Unable to authenticate with --domain-profile ($DOMAIN_PROFILE)${NC}"
    exit 1
fi

echo -e "${GREEN}✓ API Gateway account: $APIGW_ACCOUNT${NC}"
echo -e "${GREEN}✓ Route 53 account: $DOMAIN_ACCOUNT${NC}"
echo -e "${GREEN}✓ FQDN: $FQDN${NC}"
echo -e "${GREEN}✓ API endpoint: $API_ENDPOINT${NC}"
echo -e "${GREEN}✓ Region: $API_REGION${NC}"
echo ""

# Step 1: Create ACM Certificate
echo -e "${BLUE}Step 1: Creating ACM certificate...${NC}"

CERT_ARN=$(aws acm request-certificate \
    --domain-name "$FQDN" \
    --validation-method DNS \
    --region "$API_REGION" \
    --profile "$API_GATEWAY_PROFILE" \
    --query 'CertificateArn' \
    --output text)

if [ -z "$CERT_ARN" ]; then
    echo -e "${RED}❌ Error: Failed to create ACM certificate${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Created ACM certificate: $CERT_ARN${NC}"
echo -e "${YELLOW}Waiting for DNS validation records to be available...${NC}"

# Wait for certificate to have validation records
sleep 5
for i in {1..30}; do
    VALIDATION_RECORDS=$(aws acm describe-certificate \
        --certificate-arn "$CERT_ARN" \
        --region "$API_REGION" \
        --profile "$API_GATEWAY_PROFILE" \
        --query 'Certificate.DomainValidationOptions[0].ResourceRecord' \
        --output json 2>/dev/null || echo "{}")

    if [ "$VALIDATION_RECORDS" != "{}" ] && [ "$VALIDATION_RECORDS" != "null" ]; then
        break
    fi
    echo -e "${YELLOW}Waiting... (attempt $i/30)${NC}"
    sleep 2
done

VALIDATION_NAME=$(echo "$VALIDATION_RECORDS" | grep -o '"Name":"[^"]*"' | head -1 | cut -d'"' -f4)
VALIDATION_VALUE=$(echo "$VALIDATION_RECORDS" | grep -o '"Value":"[^"]*"' | head -1 | cut -d'"' -f4)

if [ -z "$VALIDATION_NAME" ] || [ -z "$VALIDATION_VALUE" ]; then
    echo -e "${RED}❌ Error: Could not retrieve DNS validation records${NC}"
    echo "Certificate ARN: $CERT_ARN"
    echo "You may need to validate manually in the AWS console"
    exit 1
fi

echo -e "${GREEN}✓ DNS validation records ready${NC}"
echo ""

# Step 2: Create DNS validation record in Route 53 (domain account)
echo -e "${BLUE}Step 2: Creating DNS validation record in Route 53...${NC}"

# Get hosted zone ID for the domain
HOSTED_ZONE_ID=$(aws route53 list-hosted-zones-by-name \
    --profile "$DOMAIN_PROFILE" \
    --query "HostedZones[?Name=='${FQDN}.'].Id" \
    --output text | cut -d'/' -f3)

if [ -z "$HOSTED_ZONE_ID" ]; then
    echo -e "${RED}❌ Error: Could not find hosted zone for $FQDN${NC}"
    echo "Make sure the domain exists in Route 53 in the domain account"
    exit 1
fi

echo -e "${GREEN}✓ Found hosted zone: $HOSTED_ZONE_ID${NC}"

# Create validation record
aws route53 change-resource-record-sets \
    --hosted-zone-id "$HOSTED_ZONE_ID" \
    --profile "$DOMAIN_PROFILE" \
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
    }" > /dev/null

echo -e "${GREEN}✓ Created DNS validation record${NC}"
echo -e "${YELLOW}Waiting for certificate validation (this can take 5-10 minutes)...${NC}"
echo ""

# Wait for certificate validation
VALIDATED=false
for i in {1..60}; do
    CERT_STATUS=$(aws acm describe-certificate \
        --certificate-arn "$CERT_ARN" \
        --region "$API_REGION" \
        --profile "$API_GATEWAY_PROFILE" \
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

# Step 3: Create custom domain in API Gateway
echo -e "${BLUE}Step 3: Creating custom domain in API Gateway...${NC}"

CUSTOM_DOMAIN=$(aws apigatewayv2 create-domain-name \
    --domain-name "$FQDN" \
    --domain-name-configurations CertificateArn="$CERT_ARN",EndpointType=REGIONAL \
    --region "$API_REGION" \
    --profile "$API_GATEWAY_PROFILE" \
    --query 'DomainNameConfigurations[0].TargetDomainName' \
    --output text 2>/dev/null || echo "")

if [ -z "$CUSTOM_DOMAIN" ]; then
    # Domain might already exist, try to describe it
    CUSTOM_DOMAIN=$(aws apigatewayv2 get-domain-names \
        --region "$API_REGION" \
        --profile "$API_GATEWAY_PROFILE" \
        --query "Items[?Name=='$FQDN'].DomainNameConfigurations[0].TargetDomainName" \
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

# Step 4: Get API ID and create mapping
echo -e "${BLUE}Step 4: Creating API mapping...${NC}"

# Extract API ID from endpoint (g1sy8uwe75.execute-api.us-west-2.amazonaws.com -> g1sy8uwe75)
API_ID=$(echo "$API_ENDPOINT" | cut -d'.' -f1)

# Check if mapping already exists
EXISTING_MAPPING=$(aws apigatewayv2 get-api-mappings \
    --domain-name "$FQDN" \
    --region "$API_REGION" \
    --profile "$API_GATEWAY_PROFILE" \
    --query "Items[0].ApiId" \
    --output text 2>/dev/null || echo "")

if [ -z "$EXISTING_MAPPING" ] || [ "$EXISTING_MAPPING" == "None" ]; then
    # Create mapping
    aws apigatewayv2 create-api-mapping \
        --domain-name "$FQDN" \
        --api-id "$API_ID" \
        --stage "\$default" \
        --region "$API_REGION" \
        --profile "$API_GATEWAY_PROFILE" > /dev/null

    echo -e "${GREEN}✓ Created API mapping${NC}"
else
    echo -e "${GREEN}✓ API mapping already exists${NC}"
fi

echo ""

# Step 5: Update Route 53 record to point to custom domain
echo -e "${BLUE}Step 5: Updating Route 53 record...${NC}"

aws route53 change-resource-record-sets \
    --hosted-zone-id "$HOSTED_ZONE_ID" \
    --profile "$DOMAIN_PROFILE" \
    --change-batch "{
        \"Changes\": [{
            \"Action\": \"UPSERT\",
            \"ResourceRecordSet\": {
                \"Name\": \"$FQDN\",
                \"Type\": \"A\",
                \"AliasTarget\": {
                    \"HostedZoneId\": \"$APIGW_ZONE_ID\",
                    \"DNSName\": \"$CUSTOM_DOMAIN\",
                    \"EvaluateTargetHealth\": false
                }
            }
        }]
    }" > /dev/null

echo -e "${GREEN}✓ Updated Route 53 record${NC}"
echo ""

# Step 6: Summary and verification
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}✅ Cross-Account DNS Setup Complete!${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""
echo -e "${GREEN}Configuration Summary:${NC}"
echo "  Domain: $FQDN"
echo "  API Gateway Account: $APIGW_ACCOUNT"
echo "  Route 53 Account: $DOMAIN_ACCOUNT"
echo "  Region: $API_REGION"
echo "  API Endpoint: $API_ENDPOINT"
echo "  Custom Domain Target: $CUSTOM_DOMAIN"
echo "  ACM Certificate ARN: $CERT_ARN"
echo "  Hosted Zone ID: $HOSTED_ZONE_ID"
echo ""
echo -e "${YELLOW}⏳ DNS Propagation${NC}"
echo "  DNS changes may take 5-15 minutes to propagate globally"
echo "  You can check status with:"
echo "    nslookup $FQDN"
echo "    dig $FQDN"
echo ""
echo -e "${YELLOW}🧪 Testing${NC}"
echo "  Once DNS propagates, test with:"
echo "    curl -I https://$FQDN/"
echo "    Or open https://$FQDN in your browser"
echo ""
echo -e "${BLUE}🔧 Troubleshooting${NC}"
echo "  View certificate status:"
echo "    aws acm describe-certificate --certificate-arn $CERT_ARN --region $API_REGION --profile $API_GATEWAY_PROFILE"
echo ""
echo "  View API mappings:"
echo "    aws apigatewayv2 get-api-mappings --domain-name $FQDN --region $API_REGION --profile $API_GATEWAY_PROFILE"
echo ""
echo "  View Route 53 records:"
echo "    aws route53 list-resource-record-sets --hosted-zone-id $HOSTED_ZONE_ID --profile $DOMAIN_PROFILE"
echo ""
