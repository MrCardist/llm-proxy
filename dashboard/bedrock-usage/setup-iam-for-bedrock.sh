#!/bin/bash

# IAM setup script for AWS Bedrock logging
# This script requires IAM permissions to create roles and policies
# Usage: AWS_PROFILE=usermanager ./setup-iam-for-bedrock.sh
#    or: ./setup-iam-for-bedrock.sh --profile usermanager

set -e  # Exit on error

echo "=========================================="
echo "AWS Bedrock IAM Setup Script"
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
    echo "  AWS_PROFILE=usermanager ./setup-iam-for-bedrock.sh"
    echo "  ./setup-iam-for-bedrock.sh --profile usermanager"
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
    echo "Make sure AWS_PROFILE is set correctly: export AWS_PROFILE=usermanager"
    exit 1
fi

CALLER_IDENTITY=$(aws sts get-caller-identity --query Arn --output text)
echo "✓ Running as: $CALLER_IDENTITY"
echo "✓ AWS Account ID: $ACCOUNT_ID"
echo ""

# Step 1: Create IAM Role for Bedrock Logging
echo "Step 1: Creating IAM Role for Bedrock logging..."
ROLE_NAME="BedrockCloudWatchLoggingRole"

# Check if role exists
if aws iam get-role --role-name "$ROLE_NAME" &> /dev/null; then
    echo "✓ IAM role $ROLE_NAME already exists"
    ROLE_EXISTS=true
else
    ROLE_EXISTS=false
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
echo ""

# Step 2: Create and attach CloudWatch Logs policy
echo "Step 2: Creating and attaching CloudWatch Logs policy..."
POLICY_NAME="BedrockCloudWatchLoggingPolicy"
POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/${POLICY_NAME}"

# Check if policy exists
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
if aws iam attach-role-policy \
    --role-name "$ROLE_NAME" \
    --policy-arn "$POLICY_ARN" 2>/dev/null; then
    echo "✓ Attached policy to role"
else
    echo "✓ Policy already attached to role"
fi
echo ""

# Step 3: Display role ARN
ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"
echo "=========================================="
echo "IAM Setup Complete!"
echo "=========================================="
echo ""
echo "Role ARN: $ROLE_ARN"
echo ""

# Step 4: Wait for IAM propagation if role was just created
if [ "$ROLE_EXISTS" = false ]; then
    echo "Waiting 15 seconds for IAM role to propagate..."
    sleep 15
    echo "✓ Done waiting"
    echo ""
fi

echo "Next step: Enable Bedrock logging with this role."
echo ""
echo "Run the following command with a profile that has Bedrock permissions:"
echo ""
echo "  ./enable-bedrock-logging.sh"
echo ""
