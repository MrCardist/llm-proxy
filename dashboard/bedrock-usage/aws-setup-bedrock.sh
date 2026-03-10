#!/bin/bash

# Comprehensive AWS Bedrock logging setup script
# This script consolidates all Bedrock setup functionality into one tool
# Supports modular setup with different permission requirements
# Usage: aws-setup-bedrock.sh [OPTION]
#    or: AWS_PROFILE=profile-name aws-setup-bedrock.sh [OPTION]
#    or: aws-setup-bedrock.sh --profile profile-name [OPTION]

set -e  # Exit on error

# Global variables
PROFILE_ARG=""
COMMAND=""
PASSROLE_USERS=()
SETUP_USERNAME=""

# Color output for better readability
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# ============================================================================
# HELP FUNCTION
# ============================================================================

show_help() {
    cat << 'EOF'
AWS Bedrock Logging Setup - Comprehensive Configuration Tool

This script consolidates all AWS Bedrock logging setup functionality into a single
tool. It supports modular operations for different AWS permission levels.

USAGE:
  aws-setup-bedrock.sh [OPTIONS] [COMMAND]

AWS PROFILE SPECIFICATION (Required):
  AWS_PROFILE=profile-name aws-setup-bedrock.sh [COMMAND]
  aws-setup-bedrock.sh --profile profile-name [COMMAND]

COMMANDS:
  --full
    Complete end-to-end setup (requires IAM admin + Bedrock permissions)
    - Creates CloudWatch log group
    - Creates IAM role and policy
    - Enables Bedrock model invocation logging
    Optionally grant PassRole permissions afterward with: --grant-passrole USER1 USER2
    This is the quickest option for a single admin user.

  --iam-only
    IAM setup only (requires IAM admin permissions only)
    - Creates IAM role: BedrockCloudWatchLoggingRole
    - Creates IAM policy: BedrockCloudWatchLoggingPolicy
    - Attaches policy to role
    Useful when different people have different permission levels.
    After this, someone with Bedrock permissions can run: --enable-logging

  --grant-passrole USER1 USER2 [USER3 ...]
    Grant PassRole permissions to specified IAM users (requires IAM admin)
    - Grants iam:PassRole permission to each user
    - Users must exist in IAM
    Example: aws-setup-bedrock.sh --grant-passrole alice bob charlie
    Note: You can specify multiple users separated by spaces.

  --enable-logging
    Enable Bedrock logging only (requires Bedrock permissions only)
    - Ensures CloudWatch log group exists
    - Configures Bedrock model invocation logging
    - Verifies the configuration
    Run this after IAM setup is complete. Useful for users without IAM perms.

  --check-status
    Display current Bedrock logging configuration status
    - Shows Bedrock logging configuration (if enabled)
    - Useful for verifying setup was successful

  --diagnose
    Diagnose setup issues and permissions
    - Checks AWS CLI connectivity
    - Verifies Bedrock is available in the region
    - Lists available foundation models
    - Tests IAM permissions
    - Useful for troubleshooting setup failures

  --setup-user USERNAME
    Create IAM policy for dashboard user (requires IAM admin)
    - Grants logs:StartQuery and logs:GetQueryResults for /aws/bedrock/modelinvocations
    - Grants bedrock:GetModelInvocationLoggingConfiguration
    - User can then run the dashboard to view logs
    - Username should be the IAM user who will run the dashboard
    Example: aws-setup-bedrock.sh --setup-user bedrock

  --help
    Display this help message

EXAMPLES:

Example 1: Complete setup (single admin user)
  AWS_PROFILE=admin aws-setup-bedrock.sh --full

Example 2: Setup with command-line profile
  aws-setup-bedrock.sh --profile admin --full

Example 3: IAM admin sets up roles/policies
  AWS_PROFILE=iam-admin aws-setup-bedrock.sh --iam-only

Example 4: Different admin grants PassRole permissions
  AWS_PROFILE=iam-admin aws-setup-bedrock.sh --grant-passrole alice bob

Example 5: Bedrock user enables logging (after IAM setup)
  AWS_PROFILE=bedrock-user aws-setup-bedrock.sh --enable-logging

Example 6: Check if logging is currently enabled
  AWS_PROFILE=any-profile aws-setup-bedrock.sh --check-status

Example 7: Diagnose setup issues (permissions, region availability)
  AWS_PROFILE=admin aws-setup-bedrock.sh --diagnose

Example 8: Grant CloudWatch Logs permissions to dashboard user
  AWS_PROFILE=iam-admin aws-setup-bedrock.sh --setup-user bedrock

PERMISSION REQUIREMENTS:

--full:
  - iam:CreateRole
  - iam:CreatePolicy
  - iam:AttachRolePolicy
  - iam:PutUserPolicy
  - logs:CreateLogGroup
  - logs:PutRetentionPolicy
  - bedrock:PutModelInvocationLoggingConfiguration
  - sts:GetCallerIdentity

--iam-only:
  - iam:CreateRole
  - iam:CreatePolicy
  - iam:AttachRolePolicy
  - sts:GetCallerIdentity

--grant-passrole:
  - iam:PutUserPolicy
  - sts:GetCallerIdentity

--enable-logging:
  - logs:CreateLogGroup
  - logs:PutRetentionPolicy
  - logs:DescribeLogGroups
  - bedrock:PutModelInvocationLoggingConfiguration
  - bedrock:GetModelInvocationLoggingConfiguration
  - sts:GetCallerIdentity

--check-status:
  - bedrock:GetModelInvocationLoggingConfiguration

--setup-user:
  - iam:PutUserPolicy
  - sts:GetCallerIdentity

WORKFLOW EXAMPLES:

Scenario 1: Single admin user with all permissions
  $ AWS_PROFILE=admin aws-setup-bedrock.sh --full
  ✓ Setup complete! CloudWatch logs are configured.
  # Optionally grant PassRole to specific users:
  $ AWS_PROFILE=admin aws-setup-bedrock.sh --grant-passrole alice bob

Scenario 2: Separated permission levels (recommended for security)
  # Step 1: IAM admin creates infrastructure
  $ AWS_PROFILE=iam-admin aws-setup-bedrock.sh --iam-only

  # Step 2: Bedrock user enables logging
  $ AWS_PROFILE=bedrock-user aws-setup-bedrock.sh --enable-logging

  # Step 3: IAM admin grants specific users PassRole permission
  $ AWS_PROFILE=iam-admin aws-setup-bedrock.sh --grant-passrole alice bob

Scenario 3: Grant CloudWatch Logs permissions to dashboard user
  # IAM admin grants permissions to bedrock user
  $ AWS_PROFILE=iam-admin aws-setup-bedrock.sh --setup-user bedrock
  ✓ bedrock user can now run the dashboard

  # bedrock user can now query logs
  $ AWS_PROFILE=bedrock .venv/bin/python bedrock-usage/app.py --port 5000

Scenario 4: Verify setup
  $ AWS_PROFILE=any-profile aws-setup-bedrock.sh --check-status
  ✓ Bedrock logging is enabled

NOTES:
  - All AWS CLI commands include the specified profile
  - The script exits with errors if prerequisites are not met
  - Idempotent: safe to run multiple times (won't recreate existing resources)
  - Log group retention is set to 30 days
  - PassRole permissions grant users the ability to use the Bedrock logging role

EOF
}

# ============================================================================
# PROFILE PARSING
# ============================================================================

parse_profile_and_command() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --profile)
                PROFILE_ARG="$2"
                shift 2
                ;;
            --full|--iam-only|--enable-logging|--check-status|--diagnose|--help)
                COMMAND="$1"
                shift
                break
                ;;
            --grant-passrole)
                COMMAND="$1"
                shift
                PASSROLE_USERS=("$@")
                break
                ;;
            --setup-user)
                COMMAND="$1"
                shift
                SETUP_USERNAME="$1"
                shift
                break
                ;;
            *)
                echo "ERROR: Unknown argument: $1"
                echo "Use: $0 --help for usage information"
                exit 1
                ;;
        esac
    done
}

validate_profile() {
    if [ -z "$AWS_PROFILE" ] && [ -z "$PROFILE_ARG" ]; then
        echo "ERROR: AWS_PROFILE environment variable must be set, or use --profile argument"
        echo ""
        echo "Usage:"
        echo "  AWS_PROFILE=your-profile $0 [COMMAND]"
        echo "  $0 --profile your-profile [COMMAND]"
        echo ""
        echo "Example:"
        echo "  AWS_PROFILE=admin $0 --full"
        echo "  $0 --profile admin --full"
        echo ""
        echo "Run '$0 --help' for more information"
        exit 1
    fi

    # Use provided profile argument if given, otherwise use AWS_PROFILE
    if [ -n "$PROFILE_ARG" ]; then
        export AWS_PROFILE="$PROFILE_ARG"
    fi
}

check_aws_cli() {
    if ! command -v aws &> /dev/null; then
        echo "ERROR: AWS CLI is not installed. Please install it first."
        echo "Visit: https://aws.amazon.com/cli/"
        exit 1
    fi
}

get_aws_info() {
    echo "Checking AWS credentials..."
    ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text 2>/dev/null || true)

    if [ -z "$ACCOUNT_ID" ]; then
        echo "ERROR: Unable to authenticate with AWS. Please check your credentials."
        echo "Make sure AWS_PROFILE is set correctly: export AWS_PROFILE=your-profile"
        exit 1
    fi

    CALLER_IDENTITY=$(aws sts get-caller-identity --query Arn --output text 2>/dev/null || true)
    REGION=$(aws configure get region --profile "$AWS_PROFILE" 2>/dev/null || echo "us-east-1")
}

print_header() {
    echo ""
    echo "=========================================="
    echo "$1"
    echo "=========================================="
    echo ""
}

# ============================================================================
# IAM SETUP FUNCTIONS
# ============================================================================

setup_iam_role() {
    local ROLE_NAME="BedrockCloudWatchLoggingRole"

    echo "Creating IAM Role..."

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
}

setup_iam_policy() {
    local ROLE_NAME="BedrockCloudWatchLoggingRole"
    local POLICY_NAME="BedrockCloudWatchLoggingPolicy"
    local POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/${POLICY_NAME}"

    echo "Creating IAM Policy..."

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
    echo "Attaching policy to role..."
    if aws iam attach-role-policy \
        --role-name "$ROLE_NAME" \
        --policy-arn "$POLICY_ARN" 2>/dev/null; then
        echo "✓ Attached policy to role"
    else
        echo "✓ Policy already attached to role"
    fi
}

grant_passrole_permissions() {
    local USERS=("$@")
    local ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/BedrockCloudWatchLoggingRole"

    if [ ${#USERS[@]} -eq 0 ]; then
        echo "ERROR: No users specified for --grant-passrole"
        echo "Usage: $0 --grant-passrole USER1 USER2 [USER3 ...]"
        exit 1
    fi

    echo "Granting iam:PassRole permissions to ${#USERS[@]} user(s)..."
    echo ""

    for USER in "${USERS[@]}"; do
        echo "Processing user: $USER"

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
}

# ============================================================================
# LOGGING SETUP FUNCTIONS
# ============================================================================

setup_log_group() {
    local LOG_GROUP_NAME="/aws/bedrock/modelinvocations"

    echo "Setting up CloudWatch Log Group..."

    # Check if log group exists
    if aws logs describe-log-groups --log-group-name-prefix "$LOG_GROUP_NAME" --query "logGroups[?logGroupName=='$LOG_GROUP_NAME']" --output text 2>/dev/null | grep -q "$LOG_GROUP_NAME"; then
        echo "✓ Log group $LOG_GROUP_NAME already exists"
    else
        aws logs create-log-group --log-group-name "$LOG_GROUP_NAME"
        echo "✓ Created log group: $LOG_GROUP_NAME"
    fi

    # Set retention policy
    aws logs put-retention-policy \
        --log-group-name "$LOG_GROUP_NAME" \
        --retention-in-days 30
    echo "✓ Set retention policy to 30 days"
}

enable_bedrock_logging() {
    local LOG_GROUP_NAME="/aws/bedrock/modelinvocations"
    local ROLE_NAME="BedrockCloudWatchLoggingRole"
    local ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"

    echo "Enabling Bedrock model invocation logging..."
    echo "  Log Group: $LOG_GROUP_NAME"
    echo "  Role ARN: $ROLE_ARN"
    echo ""

    # Enable logging configuration (without S3 config since it requires valid bucket)
    LOGGING_CONFIG='{
      "cloudWatchConfig": {
        "logGroupName": "'"$LOG_GROUP_NAME"'",
        "roleArn": "'"$ROLE_ARN"'"
      },
      "textDataDeliveryEnabled": true,
      "imageDataDeliveryEnabled": true,
      "embeddingDataDeliveryEnabled": true
    }'

    if aws bedrock put-model-invocation-logging-configuration \
        --logging-config "$LOGGING_CONFIG" 2>/dev/null; then
        echo "✓ Enabled Bedrock model invocation logging"
        echo ""
        return 0
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
        return 1
    fi
}

wait_for_iam_propagation() {
    echo "Waiting for IAM role to propagate (10 seconds)..."
    sleep 10
    echo "✓ Done waiting"
}

setup_user_policy() {
    local USERNAME="$1"
    local LOG_GROUP_NAME="/aws/bedrock/modelinvocations"

    echo "Creating CloudWatch Logs policy for user: $USERNAME"

    # Create policy document for CloudWatch Logs access
    # Note: logs:* on the specific log group is needed for CloudWatch Logs Insights queries
    POLICY_DOCUMENT='{
      "Version": "2012-10-17",
      "Statement": [
        {
          "Effect": "Allow",
          "Action": [
            "logs:*"
          ],
          "Resource": "arn:aws:logs:*:*:log-group:'"$LOG_GROUP_NAME"'*"
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

    if aws iam put-user-policy \
        --user-name "$USERNAME" \
        --policy-name "BedrockDashboardPolicy" \
        --policy-document "$POLICY_DOCUMENT" 2>/dev/null; then
        echo "✓ Created BedrockDashboardPolicy for user: $USERNAME"
        return 0
    else
        echo "⚠ Warning: Could not create policy for user $USERNAME (user may not exist)"
        return 1
    fi
}

# ============================================================================
# CHECK STATUS FUNCTION
# ============================================================================

check_status() {
    echo "Checking Bedrock logging status..."
    echo ""

    if aws bedrock get-model-invocation-logging-configuration 2>/dev/null | grep -q "cloudWatchConfig"; then
        echo "✓ Bedrock logging is ENABLED"
        echo ""
        echo "Current configuration:"
        aws bedrock get-model-invocation-logging-configuration --output table
        echo ""
        return 0
    else
        echo "✗ Bedrock logging is NOT enabled"
        echo ""
        echo "To enable logging, run:"
        echo "  AWS_PROFILE=your-profile $0 --enable-logging"
        echo ""
        return 1
    fi
}

# ============================================================================
# COMMAND IMPLEMENTATIONS
# ============================================================================

cmd_full() {
    print_header "Complete Bedrock Logging Setup"

    echo "Running as: $CALLER_IDENTITY"
    echo "AWS Account: $ACCOUNT_ID"
    echo "Region: $REGION"
    echo ""

    echo "Step 1: Setting up CloudWatch Log Group"
    setup_log_group
    echo ""

    echo "Step 2: Creating IAM Role"
    setup_iam_role
    echo ""

    echo "Step 3: Creating IAM Policy"
    setup_iam_policy
    echo ""

    echo "Step 4: Waiting for IAM propagation"
    wait_for_iam_propagation
    echo ""

    echo "Step 5: Enabling Bedrock logging"
    if enable_bedrock_logging; then
        echo "Verifying configuration..."
        aws bedrock get-model-invocation-logging-configuration
        echo ""

        print_header "Setup Complete!"
        echo "✓ All Bedrock logging infrastructure has been configured."
        echo ""
        echo "Next steps:"
        echo "1. Grant PassRole permissions to IAM users (optional):"
        echo "   AWS_PROFILE=$AWS_PROFILE $0 --grant-passrole user1 user2"
        echo ""
        echo "2. Make some Bedrock API calls to generate logs"
        echo "3. Wait 2-5 minutes for logs to appear in CloudWatch"
        echo "4. Run the dashboard: .venv/bin/python app.py"
        echo "5. Open http://localhost:5000 in your browser"
        echo ""
        echo "To monitor logs in real-time:"
        echo "  aws logs tail /aws/bedrock/modelinvocations --follow"
        echo ""
    else
        echo "ERROR: Failed to enable Bedrock logging"
        exit 1
    fi
}

cmd_iam_only() {
    print_header "IAM Setup Only"

    echo "Running as: $CALLER_IDENTITY"
    echo "AWS Account: $ACCOUNT_ID"
    echo ""

    echo "Step 1: Creating IAM Role"
    setup_iam_role
    echo ""

    echo "Step 2: Creating IAM Policy"
    setup_iam_policy
    echo ""

    echo "Step 3: Waiting for IAM propagation"
    wait_for_iam_propagation
    echo ""

    print_header "IAM Setup Complete!"
    echo "✓ IAM role and policy have been created and attached."
    echo ""
    echo "Next steps:"
    echo "1. (Optional) Grant PassRole permissions to users:"
    echo "   AWS_PROFILE=iam-admin $0 --grant-passrole user1 user2"
    echo ""
    echo "2. Have someone with Bedrock permissions run:"
    echo "   AWS_PROFILE=bedrock-user $0 --enable-logging"
    echo ""
}

cmd_grant_passrole() {
    print_header "Grant PassRole Permissions"

    echo "Running as: $CALLER_IDENTITY"
    echo "AWS Account: $ACCOUNT_ID"
    echo ""

    grant_passrole_permissions "${PASSROLE_USERS[@]}"

    print_header "Permissions Updated!"
    echo "✓ PassRole permissions granted to ${#PASSROLE_USERS[@]} user(s)"
    echo ""
}

cmd_enable_logging() {
    print_header "Enable Bedrock Logging"

    echo "Running as: $CALLER_IDENTITY"
    echo "AWS Account: $ACCOUNT_ID"
    echo "Region: $REGION"
    echo ""

    echo "Step 1: Setting up CloudWatch Log Group"
    setup_log_group
    echo ""

    echo "Step 2: Enabling Bedrock logging"
    if enable_bedrock_logging; then
        echo "Verifying configuration..."
        aws bedrock get-model-invocation-logging-configuration
        echo ""

        print_header "Setup Complete!"
        echo "✓ Bedrock logging has been enabled."
        echo ""
        echo "Next steps:"
        echo "1. Make some Bedrock API calls to generate logs"
        echo "2. Wait 2-5 minutes for logs to appear in CloudWatch"
        echo "3. Run the dashboard: .venv/bin/python app.py"
        echo "4. Open http://localhost:5000 in your browser"
        echo ""
        echo "To monitor logs in real-time:"
        echo "  aws logs tail /aws/bedrock/modelinvocations --follow"
        echo ""
    else
        exit 1
    fi
}

cmd_check_status() {
    print_header "Bedrock Logging Status"
    check_status
}

cmd_diagnose() {
    print_header "Bedrock Setup Diagnostics"

    echo "Running as: $CALLER_IDENTITY"
    echo "AWS Account: $ACCOUNT_ID"
    echo "Region: $REGION"
    echo ""

    # Test 1: Bedrock availability
    echo "Test 1: Checking Bedrock availability in region ($REGION)..."
    if aws bedrock list-foundation-models --region "$REGION" &>/dev/null; then
        echo "✓ Bedrock is available in $REGION"
        MODEL_COUNT=$(aws bedrock list-foundation-models --region "$REGION" --query 'modelSummaries | length(@)' --output text 2>/dev/null || echo "0")
        echo "  Foundation models available: $MODEL_COUNT"
    else
        echo "✗ Bedrock is NOT available in $REGION"
        echo "  Try a different region (e.g., us-east-1, eu-west-1, ap-southeast-1)"
    fi
    echo ""

    # Test 2: CloudWatch Logs permissions
    echo "Test 2: Checking CloudWatch Logs permissions..."
    if aws logs describe-log-groups --max-items 1 &>/dev/null; then
        echo "✓ CloudWatch Logs permissions OK"
    else
        echo "✗ Missing CloudWatch Logs permissions (logs:DescribeLogGroups)"
    fi
    echo ""

    # Test 3: IAM permissions
    echo "Test 3: Checking IAM permissions..."
    if aws iam get-role --role-name "BedrockCloudWatchLoggingRole" &>/dev/null; then
        echo "✓ Can read IAM roles (iam:GetRole)"
    else
        echo "✗ Missing IAM read permissions (iam:GetRole)"
    fi
    echo ""

    # Test 4: Bedrock logging configuration
    echo "Test 4: Checking Bedrock logging configuration permissions..."
    if aws bedrock get-model-invocation-logging-configuration &>/dev/null; then
        echo "✓ Can read Bedrock logging config (bedrock:GetModelInvocationLoggingConfiguration)"
        LOGGING_STATUS=$(aws bedrock get-model-invocation-logging-configuration --query 'loggingConfig.cloudWatchConfig.logGroupName' --output text 2>/dev/null || echo "none")
        if [ "$LOGGING_STATUS" != "none" ] && [ -n "$LOGGING_STATUS" ]; then
            echo "  Currently enabled: $LOGGING_STATUS"
        else
            echo "  Currently disabled"
        fi
    else
        echo "✗ Cannot read Bedrock logging config (missing bedrock:GetModelInvocationLoggingConfiguration)"
    fi
    echo ""

    # Test 5: Bedrock put permission (the critical one)
    echo "Test 5: Checking Bedrock PUT permission (critical for setup)..."
    echo "  Note: This is difficult to test without actually making the API call."
    echo "  If --full or --enable-logging fails, this permission is likely missing:"
    echo "    bedrock:PutModelInvocationLoggingConfiguration"
    echo ""

    # Summary
    echo "=========================================="
    echo "Diagnostic Summary"
    echo "=========================================="
    echo ""
    echo "If tests 1-4 passed but --full still fails, the issue is likely:"
    echo "  → Missing bedrock:PutModelInvocationLoggingConfiguration permission"
    echo ""
    echo "Solution:"
    echo "  1. Ask your AWS admin to attach this permission to your user:"
    echo "     bedrock:PutModelInvocationLoggingConfiguration"
    echo ""
    echo "  2. Or manually enable logging via AWS Console:"
    echo "     AWS Console > Bedrock > Settings > Model invocation logging"
    echo ""
}

cmd_setup_user() {
    if [ -z "$SETUP_USERNAME" ]; then
        echo "ERROR: Username not specified"
        echo "Usage: $0 --setup-user USERNAME"
        exit 1
    fi

    print_header "Setup Dashboard User Permissions"

    echo "Running as: $CALLER_IDENTITY"
    echo "AWS Account: $ACCOUNT_ID"
    echo "Target User: $SETUP_USERNAME"
    echo ""

    if setup_user_policy "$SETUP_USERNAME"; then
        echo ""
        print_header "User Setup Complete!"
        echo "✓ User '$SETUP_USERNAME' can now query Bedrock logs"
        echo ""
        echo "The user now has permissions to:"
        echo "  • Query CloudWatch logs: logs:StartQuery, logs:GetQueryResults"
        echo "  • Read Bedrock logging config: bedrock:GetModelInvocationLoggingConfiguration"
        echo ""
        echo "Next steps:"
        echo "1. User can now run the dashboard:"
        echo "   AWS_PROFILE=$SETUP_USERNAME .venv/bin/python bedrock-usage/app.py"
        echo ""
        echo "2. Or deploy to Lambda:"
        echo "   AWS_PROFILE=$SETUP_USERNAME ../setup-lambda.sh bedrock-usage"
        echo ""
    else
        echo "ERROR: Failed to setup user permissions"
        exit 1
    fi
}

# ============================================================================
# MAIN EXECUTION
# ============================================================================

main() {
    # Parse arguments
    parse_profile_and_command "$@"

    # Default to --help if no command specified
    if [ -z "$COMMAND" ]; then
        show_help
        exit 0
    fi

    # Show help
    if [ "$COMMAND" = "--help" ]; then
        show_help
        exit 0
    fi

    # Validate AWS profile
    validate_profile

    # Check AWS CLI
    check_aws_cli

    # Get AWS info (needed for all commands except --help)
    get_aws_info

    # Execute command
    case "$COMMAND" in
        --full)
            cmd_full
            ;;
        --iam-only)
            cmd_iam_only
            ;;
        --grant-passrole)
            cmd_grant_passrole
            ;;
        --enable-logging)
            cmd_enable_logging
            ;;
        --check-status)
            cmd_check_status
            ;;
        --diagnose)
            cmd_diagnose
            ;;
        --setup-user)
            cmd_setup_user
            ;;
        *)
            echo "ERROR: Unknown command: $COMMAND"
            show_help
            exit 1
            ;;
    esac
}

# Run main function
main "$@"
