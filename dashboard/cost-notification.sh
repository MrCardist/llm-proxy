#!/bin/bash

# Setup AWS cost notifications via email
# Uses AWS Budgets and Billing Alerts with SNS email subscriptions
# Usage: ./cost-notification.sh --profile <aws-profile> --dollar <threshold> --to <email1,email2,...>

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
AWS_PROFILE="${AWS_PROFILE:-default}"
DOLLAR_THRESHOLDS=""
EMAIL_ADDRESSES=""

# Parse command-line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --profile)
            AWS_PROFILE="$2"
            shift 2
            ;;
        --dollar)
            DOLLAR_THRESHOLDS="$2"
            shift 2
            ;;
        --to)
            EMAIL_ADDRESSES="$2"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1"
            echo ""
            echo "Usage: $0 --profile <profile> --dollar <thresholds> --to <email1,email2,...>"
            echo ""
            echo "Options:"
            echo "  --profile <profile>     AWS profile to use (default: AWS_PROFILE env var or 'default')"
            echo "  --dollar <thresholds>   Comma-separated cost thresholds in USD (e.g., 100,250,500)"
            echo "  --to <emails>           Comma-separated email addresses (e.g., user@example.com,admin@example.com)"
            echo ""
            echo "Examples:"
            echo "  ./cost-notification.sh --profile prod --dollar 100,500,1000 --to admin@example.com"
            echo "  ./cost-notification.sh --profile default --dollar 50,100,250.50 --to user1@example.com,user2@example.com"
            exit 1
            ;;
    esac
done

# Validate required arguments
if [ -z "$DOLLAR_THRESHOLDS" ]; then
    echo -e "${RED}❌ Error: --dollar argument is required${NC}"
    exit 1
fi

if [ -z "$EMAIL_ADDRESSES" ]; then
    echo -e "${RED}❌ Error: --to argument is required${NC}"
    exit 1
fi

# Validate dollar thresholds - each should be a valid number
IFS=',' read -ra THRESHOLD_ARRAY <<< "$DOLLAR_THRESHOLDS"
for threshold in "${THRESHOLD_ARRAY[@]}"; do
    threshold=$(echo "$threshold" | xargs)  # Trim whitespace
    if ! [[ "$threshold" =~ ^[0-9]+(\.[0-9]{1,2})?$ ]]; then
        echo -e "${RED}❌ Error: Invalid dollar amount: $threshold${NC}"
        exit 1
    fi
done

# Sort thresholds in ascending order for better UX
SORTED_THRESHOLDS=$(printf '%s\n' "${THRESHOLD_ARRAY[@]}" | sort -n | paste -sd ',' -)
THRESHOLD_ARRAY=(${SORTED_THRESHOLDS//,/ })

echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}AWS Cost Notification Setup${NC}"
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
    echo "Make sure --profile is set correctly and credentials are available"
    exit 1
fi

REGION=$(aws configure get region --profile "$AWS_PROFILE" || echo "us-east-1")
echo -e "${GREEN}✓ Account ID: $ACCOUNT_ID${NC}"
echo -e "${GREEN}✓ Region: $REGION${NC}"
echo -e "${GREEN}✓ Profile: $AWS_PROFILE${NC}"
echo -e "${GREEN}✓ Cost Thresholds: \$${SORTED_THRESHOLDS}${NC}"
echo -e "${GREEN}✓ Email Addresses: $EMAIL_ADDRESSES${NC}"
echo ""

# Convert email list to array
IFS=',' read -ra EMAILS <<< "$EMAIL_ADDRESSES"

# Step 1: Create SNS topic for billing alerts
echo -e "${BLUE}Step 1: Creating SNS topic for billing alerts...${NC}"

TOPIC_NAME="aws-cost-alerts-${ACCOUNT_ID}"
TOPIC_ARN=$(aws sns list-topics --profile "$AWS_PROFILE" --region "$REGION" \
    --query "Topics[?contains(TopicArn, '$TOPIC_NAME')].TopicArn" --output text 2>/dev/null || true)

if [ -z "$TOPIC_ARN" ]; then
    echo -e "${YELLOW}Creating new SNS topic: $TOPIC_NAME${NC}"
    TOPIC_ARN=$(aws sns create-topic \
        --name "$TOPIC_NAME" \
        --profile "$AWS_PROFILE" \
        --region "$REGION" \
        --query 'TopicArn' \
        --output text)
    echo -e "${GREEN}✓ Created SNS topic: $TOPIC_ARN${NC}"
else
    echo -e "${GREEN}✓ Using existing SNS topic: $TOPIC_ARN${NC}"
fi
echo ""

# Step 2: Subscribe email addresses to SNS topic
echo -e "${BLUE}Step 2: Subscribing email addresses to SNS topic...${NC}"

for email in "${EMAILS[@]}"; do
    email=$(echo "$email" | xargs)  # Trim whitespace

    # Check if subscription already exists
    EXISTING_SUB=$(aws sns list-subscriptions-by-topic \
        --topic-arn "$TOPIC_ARN" \
        --profile "$AWS_PROFILE" \
        --region "$REGION" \
        --query "Subscriptions[?Endpoint=='$email'].SubscriptionArn" \
        --output text 2>/dev/null || true)

    if [ -z "$EXISTING_SUB" ] || [ "$EXISTING_SUB" == "PendingConfirmation" ]; then
        echo -e "${YELLOW}Subscribing $email...${NC}"
        aws sns subscribe \
            --topic-arn "$TOPIC_ARN" \
            --protocol email \
            --notification-endpoint "$email" \
            --profile "$AWS_PROFILE" \
            --region "$REGION" > /dev/null
        echo -e "${GREEN}✓ Subscription pending confirmation for $email${NC}"
        echo -e "${YELLOW}  (User will receive confirmation email - must click link to activate)${NC}"
    else
        echo -e "${GREEN}✓ $email already subscribed${NC}"
    fi
done
echo ""

# Step 3: Create AWS Budget alerts for each threshold
echo -e "${BLUE}Step 3: Creating AWS Budget cost alerts...${NC}"

CREATED_BUDGETS=()

for threshold in "${THRESHOLD_ARRAY[@]}"; do
    threshold=$(echo "$threshold" | xargs)  # Trim whitespace

    BUDGET_NAME="cost-alert-${threshold}usd-${ACCOUNT_ID}"

    # Check if budget already exists
    EXISTING_BUDGET=$(aws budgets describe-budgets \
        --account-id "$ACCOUNT_ID" \
        --profile "$AWS_PROFILE" \
        --query "Budgets[?BudgetName=='$BUDGET_NAME'].BudgetName" \
        --output text 2>/dev/null || true)

    if [ -z "$EXISTING_BUDGET" ]; then
        echo -e "${YELLOW}Creating AWS Budget: $BUDGET_NAME${NC}"

        # Create budget with alert at 100% (will trigger when actual spend reaches threshold)
        aws budgets create-budget \
            --account-id "$ACCOUNT_ID" \
            --budget BudgetName="$BUDGET_NAME",BudgetLimit="{Amount='$threshold',Unit='USD'}",TimeUnit='MONTHLY',BudgetType='COST' \
            --notifications-with-subscribers '[{
                "Notification": {
                    "NotificationType": "ACTUAL",
                    "ComparisonOperator": "GREATER_THAN_OR_EQUAL_TO",
                    "Threshold": 100
                },
                "Subscribers": [
                    {
                        "SubscriptionType": "SNS",
                        "Address": "'"$TOPIC_ARN"'"
                    }
                ]
            },
            {
                "Notification": {
                    "NotificationType": "FORECASTED",
                    "ComparisonOperator": "GREATER_THAN_OR_EQUAL_TO",
                    "Threshold": 100
                },
                "Subscribers": [
                    {
                        "SubscriptionType": "SNS",
                        "Address": "'"$TOPIC_ARN"'"
                    }
                ]
            }]' \
            --profile "$AWS_PROFILE" > /dev/null

        echo -e "${GREEN}✓ Created AWS Budget: $BUDGET_NAME${NC}"
        CREATED_BUDGETS+=("$BUDGET_NAME")
    else
        echo -e "${GREEN}✓ AWS Budget already exists: $BUDGET_NAME${NC}"
        CREATED_BUDGETS+=("$BUDGET_NAME")
    fi
done
echo ""

# Step 4: Enable Billing Alerts (legacy CloudWatch approach as backup)
echo -e "${BLUE}Step 4: Setting up CloudWatch Billing Alerts (backup)...${NC}"

CREATED_ALARMS=()

for threshold in "${THRESHOLD_ARRAY[@]}"; do
    threshold=$(echo "$threshold" | xargs)  # Trim whitespace

    ALARM_NAME="billing-alert-${threshold}usd"

    # Check if alarm already exists
    EXISTING_ALARM=$(aws cloudwatch describe-alarms \
        --alarm-names "$ALARM_NAME" \
        --profile "$AWS_PROFILE" \
        --query "MetricAlarms[0].AlarmName" \
        --output text 2>/dev/null || true)

    if [ "$EXISTING_ALARM" != "$ALARM_NAME" ]; then
        echo -e "${YELLOW}Creating CloudWatch billing alarm: $ALARM_NAME${NC}"

        # Enable detailed billing metrics (required for CloudWatch alarms)
        aws ce enable-cost-category-definition \
            --profile "$AWS_PROFILE" > /dev/null 2>&1 || true

        # Create CloudWatch alarm
        aws cloudwatch put-metric-alarm \
            --alarm-name "$ALARM_NAME" \
            --alarm-description "Alert when estimated monthly charges reach \$$threshold" \
            --metric-name EstimatedCharges \
            --namespace AWS/Billing \
            --statistic Maximum \
            --period 300 \
            --evaluation-periods 1 \
            --threshold "$threshold" \
            --comparison-operator GreaterThanOrEqualToThreshold \
            --alarm-actions "$TOPIC_ARN" \
            --profile "$AWS_PROFILE" > /dev/null

        echo -e "${GREEN}✓ Created CloudWatch billing alarm: $ALARM_NAME${NC}"
        CREATED_ALARMS+=("$ALARM_NAME")
    else
        echo -e "${GREEN}✓ CloudWatch alarm already exists: $ALARM_NAME${NC}"
        CREATED_ALARMS+=("$ALARM_NAME")
    fi
done
echo ""

# Step 5: Display summary and next steps
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}✅ Cost Notification Setup Complete!${NC}"
echo -e "${BLUE}════════════════════════════════════════════════════════════${NC}"
echo ""
echo -e "${GREEN}Configuration Summary:${NC}"
echo "  Account ID: $ACCOUNT_ID"
echo "  Region: $REGION"
echo "  Cost Thresholds: \$${SORTED_THRESHOLDS}/month"
echo "  SNS Topic: $TOPIC_ARN"
echo ""
echo -e "${GREEN}AWS Budgets Created (${#CREATED_BUDGETS[@]}):${NC}"
for budget in "${CREATED_BUDGETS[@]}"; do
    echo "  ✓ $budget"
done
echo ""
echo -e "${GREEN}CloudWatch Alarms Created (${#CREATED_ALARMS[@]}):${NC}"
for alarm in "${CREATED_ALARMS[@]}"; do
    echo "  ✓ $alarm"
done
echo ""
echo -e "${YELLOW}📧 Email Subscriptions:${NC}"
for email in "${EMAILS[@]}"; do
    email=$(echo "$email" | xargs)
    echo "  ✓ $email"
done
echo ""
echo -e "${YELLOW}⚠️  IMPORTANT - Next Steps:${NC}"
echo "  1. Check your email inboxes for AWS SNS subscription confirmation"
echo "  2. Click the confirmation link in each email to activate subscriptions"
echo "  3. Without confirmation, you will NOT receive cost alerts"
echo ""
echo -e "${BLUE}📊 Alert Details:${NC}"
echo "  • AWS Budget alerts trigger when actual/forecasted spend reaches:"
for threshold in "${THRESHOLD_ARRAY[@]}"; do
    echo "    - \$$(echo "$threshold" | xargs) per month"
done
echo "  • CloudWatch billing alarms trigger when estimated charges reach:"
for threshold in "${THRESHOLD_ARRAY[@]}"; do
    echo "    - \$$(echo "$threshold" | xargs)"
done
echo ""
echo -e "${BLUE}🔧 To manage notifications:${NC}"
echo "  View SNS topic subscriptions:"
echo "    aws sns list-subscriptions-by-topic --topic-arn $TOPIC_ARN --profile $AWS_PROFILE"
echo ""
echo "  View AWS Budgets:"
echo "    aws budgets describe-budgets --account-id $ACCOUNT_ID --profile $AWS_PROFILE"
echo ""
echo "  Delete a budget:"
echo "    aws budgets delete-budget --account-id $ACCOUNT_ID --budget-name <budget-name> --profile $AWS_PROFILE"
echo ""
echo "  View CloudWatch alarms:"
echo "    aws cloudwatch describe-alarms --profile $AWS_PROFILE"
echo ""
echo "  Delete a CloudWatch alarm:"
echo "    aws cloudwatch delete-alarms --alarm-names <alarm-name> --profile $AWS_PROFILE"
echo ""
