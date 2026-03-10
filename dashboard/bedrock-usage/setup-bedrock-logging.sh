#!/bin/bash

# Setup script to enable AWS Bedrock model invocation logging
# Usage: AWS_PROFILE=dirkcli ./setup-bedrock-logging.sh
#    or: ./setup-bedrock-logging.sh --profile dirkcli

set -e  # Exit on error

echo "=========================================="
echo "AWS Bedrock Logging Setup Script"
echo "=========================================="
echo ""

# Parse command-line arguments for --profile flag
PROFILE_ARG=""
while [[ $# -gt 0 ]]; do
    case $1 in
        --profile)
            PROFILE_ARG="$2"
            shift 2
            ;;
        *)
            echo "ERROR: Unknown argument: $1"
            echo "Usage: $0 [--profile PROFILE_NAME]"
            exit 1
            ;;
    esac
done

# Check if AWS_PROFILE is set, or use --profile argument if provided
if [ -z "$AWS_PROFILE" ] && [ -z "$PROFILE_ARG" ]; then
    echo "ERROR: AWS_PROFILE environment variable must be set, or use --profile argument"
    echo ""
    echo "Usage:"
    echo "  AWS_PROFILE=your-profile $0"
    echo "  $0 --profile your-profile"
    echo ""
    echo "Example:"
    echo "  AWS_PROFILE=dirkcli ./setup-bedrock-logging.sh"
    echo "  ./setup-bedrock-logging.sh --profile dirkcli"
    exit 1
fi

# Use provided profile argument if given, otherwise use AWS_PROFILE
if [ -n "$PROFILE_ARG" ]; then
    export AWS_PROFILE="$PROFILE_ARG"
fi

# Check if AWS CLI is installed
if ! command -v aws &> /dev/null; then
    echo "ERROR: AWS CLI is not installed. Please install it first."
    echo "Visit: https://aws.amazon.com/cli/"
    exit 1
fi

# Get AWS account info
echo "Checking AWS credentials..."
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text 2>/dev/null || true)

if [ -z "$ACCOUNT_ID" ]; then
    echo "ERROR: Unable to authenticate with AWS. Please check your credentials."
    echo "Make sure AWS_PROFILE is set correctly: export AWS_PROFILE=dirkcli"
    exit 1
fi

REGION=$(aws configure get region || echo "us-east-1")
echo "✓ AWS Account ID: $ACCOUNT_ID"
echo "✓ Region: $REGION"
echo ""

# Step 1: Create CloudWatch Log Group
echo "Step 1: Creating CloudWatch Log Group..."
LOG_GROUP_NAME="/aws/bedrock/modelinvocations"

if aws logs describe-log-groups --log-group-name-prefix "$LOG_GROUP_NAME" --query "logGroups[?logGroupName=='$LOG_GROUP_NAME']" --output text 2>/dev/null | grep -q "$LOG_GROUP_NAME"; then
    echo "✓ Log group $LOG_GROUP_NAME already exists"
else
    aws logs create-log-group --log-group-name "$LOG_GROUP_NAME"
    echo "✓ Created log group: $LOG_GROUP_NAME"
fi

# Set retention policy (optional, keep logs for 30 days)
aws logs put-retention-policy \
    --log-group-name "$LOG_GROUP_NAME" \
    --retention-in-days 30
echo "✓ Set retention policy to 30 days"
echo ""

# Step 2: Create IAM Role for Bedrock Logging
echo "Step 2: Creating IAM Role for Bedrock logging..."
ROLE_NAME="BedrockCloudWatchLoggingRole"

# Check if role exists
if aws iam get-role --role-name "$ROLE_NAME" &> /dev/null; then
    echo "✓ IAM role $ROLE_NAME already exists"
else
    # Create trust policy
    TRUST_POLICY='{
      "Version": "2012-10-17",
      "Statement": [
        {
          "Effect": "Allow",
          "Principal": {
            "Service": "bedrock.amazonaws.com"
          },
          "Action": "sts:AssumeRole"
        }
      ]
    }'

    aws iam create-role \
        --role-name "$ROLE_NAME" \
        --assume-role-policy-document "$TRUST_POLICY" \
        --description "Role for Bedrock to write logs to CloudWatch"
    echo "✓ Created IAM role: $ROLE_NAME"
fi

# Step 3: Attach CloudWatch Logs policy to the role
echo "Step 3: Attaching CloudWatch Logs policy..."
POLICY_NAME="BedrockCloudWatchLoggingPolicy"

# Check if policy exists
POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/${POLICY_NAME}"

if aws iam get-policy --policy-arn "$POLICY_ARN" &> /dev/null; then
    echo "✓ Policy $POLICY_NAME already exists"
else
    # Create policy document
    POLICY_DOCUMENT='{
      "Version": "2012-10-17",
      "Statement": [
        {
          "Effect": "Allow",
          "Action": [
            "logs:CreateLogStream",
            "logs:PutLogEvents"
          ],
          "Resource": "arn:aws:logs:*:*:log-group:/aws/bedrock/*"
        }
      ]
    }'

    aws iam create-policy \
        --policy-name "$POLICY_NAME" \
        --policy-document "$POLICY_DOCUMENT" \
        --description "Policy for Bedrock to write to CloudWatch Logs"
    echo "✓ Created IAM policy: $POLICY_NAME"
fi

# Attach policy to role
aws iam attach-role-policy \
    --role-name "$ROLE_NAME" \
    --policy-arn "$POLICY_ARN" 2>/dev/null || echo "✓ Policy already attached"
echo "✓ Attached policy to role"
echo ""

# Step 4: Configure Bedrock Model Invocation Logging
echo "Step 4: Enabling Bedrock model invocation logging..."
ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"

# Wait a few seconds for IAM role to propagate
echo "Waiting for IAM role to propagate (10 seconds)..."
sleep 10

# Enable logging configuration
LOGGING_CONFIG='{
  "cloudWatchConfig": {
    "logGroupName": "'"$LOG_GROUP_NAME"'",
    "roleArn": "'"$ROLE_ARN"'",
    "largeDataDeliveryS3Config": {
      "bucketName": "",
      "keyPrefix": ""
    }
  },
  "textDataDeliveryEnabled": true,
  "imageDataDeliveryEnabled": true,
  "embeddingDataDeliveryEnabled": true
}'

# Try to update logging configuration
if aws bedrock put-model-invocation-logging-configuration \
    --logging-config "$LOGGING_CONFIG" 2>/dev/null; then
    echo "✓ Enabled Bedrock model invocation logging"
else
    echo "⚠ Warning: Failed to enable Bedrock logging configuration."
    echo "This might be due to:"
    echo "  1. Bedrock service not available in this region ($REGION)"
    echo "  2. Insufficient permissions"
    echo "  3. IAM role not yet propagated (wait a few minutes and try again)"
    echo ""
    echo "You can manually enable it in AWS Console:"
    echo "  AWS Console > Bedrock > Settings > Model invocation logging"
    echo ""
fi

echo ""
echo "=========================================="
echo "Setup Complete!"
echo "=========================================="
echo ""
echo "Next steps:"
echo "1. Make some Bedrock API calls to generate logs"
echo "2. Wait a few minutes for logs to appear in CloudWatch"
echo "3. Run your Flask dashboard: .venv/bin/python app.py"
echo "4. Open http://localhost:5000 in your browser"
echo ""
echo "To verify logs are being created:"
echo "  aws logs tail $LOG_GROUP_NAME --follow"
echo ""
