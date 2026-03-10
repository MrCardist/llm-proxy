#!/bin/bash

# Enable Bedrock model invocation logging
# This script requires Bedrock permissions
# Usage: AWS_PROFILE=dirkcli ./enable-bedrock-logging.sh
#    or: ./enable-bedrock-logging.sh --profile dirkcli

set -e  # Exit on error

echo "=========================================="
echo "Enable Bedrock Model Invocation Logging"
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
    echo "  AWS_PROFILE=dirkcli ./enable-bedrock-logging.sh"
    echo "  ./enable-bedrock-logging.sh --profile dirkcli"
    exit 1
fi

# Use provided profile argument if given, otherwise use AWS_PROFILE
if [ -n "$PROFILE_ARG" ]; then
    export AWS_PROFILE="$PROFILE_ARG"
fi

# Check if AWS CLI is installed
if ! command -v aws &> /dev/null; then
    echo "ERROR: AWS CLI is not installed. Please install it first."
    exit 1
fi

# Get AWS account info
echo "Checking AWS credentials..."
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text 2>/dev/null || true)

if [ -z "$ACCOUNT_ID" ]; then
    echo "ERROR: Unable to authenticate with AWS."
    exit 1
fi

CALLER_IDENTITY=$(aws sts get-caller-identity --query Arn --output text)
REGION=$(aws configure get region || echo "us-west-2")

echo "✓ Running as: $CALLER_IDENTITY"
echo "✓ AWS Account ID: $ACCOUNT_ID"
echo "✓ Region: $REGION"
echo ""

# Define resources
LOG_GROUP_NAME="/aws/bedrock/modelinvocations"
ROLE_NAME="BedrockCloudWatchLoggingRole"
ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"

echo "Configuring Bedrock logging..."
echo "  Log Group: $LOG_GROUP_NAME"
echo "  Role ARN: $ROLE_ARN"
echo ""

# Check if log group exists
if ! aws logs describe-log-groups --log-group-name-prefix "$LOG_GROUP_NAME" --query "logGroups[?logGroupName=='$LOG_GROUP_NAME']" --output text 2>/dev/null | grep -q "$LOG_GROUP_NAME"; then
    echo "WARNING: Log group $LOG_GROUP_NAME does not exist!"
    echo "Creating it now..."
    aws logs create-log-group --log-group-name "$LOG_GROUP_NAME"
    aws logs put-retention-policy --log-group-name "$LOG_GROUP_NAME" --retention-in-days 30
    echo "✓ Created log group"
    echo ""
fi

# Enable Bedrock logging
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

if aws bedrock put-model-invocation-logging-configuration \
    --logging-config "$LOGGING_CONFIG" 2>/dev/null; then
    echo "✓ Successfully enabled Bedrock model invocation logging!"
    echo ""

    # Verify configuration
    echo "Verifying configuration..."
    aws bedrock get-model-invocation-logging-configuration
    echo ""

    echo "=========================================="
    echo "Setup Complete!"
    echo "=========================================="
    echo ""
    echo "Next steps:"
    echo "1. Make some Bedrock API calls to generate logs"
    echo "2. Wait 2-5 minutes for logs to appear in CloudWatch"
    echo "3. Run your Flask dashboard: .venv/bin/python app.py"
    echo "4. Open http://localhost:5000 in your browser"
    echo ""
    echo "To monitor logs in real-time:"
    echo "  aws logs tail $LOG_GROUP_NAME --follow"
    echo ""
else
    echo "ERROR: Failed to enable Bedrock logging configuration."
    echo ""
    echo "Possible reasons:"
    echo "  1. Bedrock service not available in region: $REGION"
    echo "  2. Insufficient permissions for bedrock:PutModelInvocationLoggingConfiguration"
    echo "  3. IAM role not yet propagated (wait a few minutes and try again)"
    echo ""
    echo "You can manually enable it in AWS Console:"
    echo "  AWS Console > Bedrock > Settings > Model invocation logging"
    echo "  Use Role ARN: $ROLE_ARN"
    echo ""
    exit 1
fi
