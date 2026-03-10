#!/bin/bash

# Grant iam:PassRole permission to users who need to enable Bedrock logging
# Usage: AWS_PROFILE=usermanager ./grant-passrole-permission.sh
#    or: ./grant-passrole-permission.sh --profile usermanager

set -e

echo "=========================================="
echo "Grant PassRole Permission for Bedrock"
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
    echo "  AWS_PROFILE=usermanager ./grant-passrole-permission.sh"
    echo "  ./grant-passrole-permission.sh --profile usermanager"
    exit 1
fi

# Use provided profile argument if given, otherwise use AWS_PROFILE
if [ -n "$PROFILE_ARG" ]; then
    export AWS_PROFILE="$PROFILE_ARG"
fi

ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/BedrockCloudWatchLoggingRole"

echo "✓ AWS Account ID: $ACCOUNT_ID"
echo ""

# Create inline policy for IAM users to allow PassRole
USERS=("aider" "dirkcli")

for USER in "${USERS[@]}"; do
    echo "Granting iam:PassRole permission to user: $USER"

    POLICY_DOCUMENT='{
      "Version": "2012-10-17",
      "Statement": [
        {
          "Effect": "Allow",
          "Action": "iam:PassRole",
          "Resource": "'"$ROLE_ARN"'"
        }
      ]
    }'

    if aws iam put-user-policy \
        --user-name "$USER" \
        --policy-name "BedrockPassRolePolicy" \
        --policy-document "$POLICY_DOCUMENT" 2>/dev/null; then
        echo "✓ Granted PassRole permission to $USER"
    else
        echo "⚠ Warning: Could not grant permission to $USER (user may not exist)"
    fi
    echo ""
done

echo "=========================================="
echo "Permissions Updated!"
echo "=========================================="
echo ""
echo "Now you can enable Bedrock logging with:"
echo "  AWS_PROFILE=bedrock ./enable-bedrock-logging.sh"
echo ""
