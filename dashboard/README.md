# Dashboards

A collection of dashboards that help track usage and costs across AWS services and AI platforms. Currently includes Bedrock usage tracking, with support for adding more dashboards.

<img width="1017" height="1154" alt="image" src="https://github.com/user-attachments/assets/00f51e04-ea25-4926-a34e-829fb5bcc827" />

## Features

- **Real-time monitoring** of AWS service usage and costs
- **Multi-user tracking** with aggregation and mapping capabilities
- **Interactive dashboards** with charts and detailed analytics
- **Flexible deployment** options (local, systemd service, or AWS Lambda)

## Bedrock Usage Dashboard

As using Claude Code on a per-token basis can be expensive, it's important to track usage thoroughly. The Bedrock dashboard provides:

- **Invocation tracking**: Monitor API calls per user and model
- **Token usage**: Track input and output tokens separately
- **Cost analysis**: See costs broken down by user and model
- **Time-series trends**: Daily cost and usage patterns
- **Model pricing**: Configurable pricing for all Bedrock models
- **User mapping**: Aggregate usage across multiple user identities

## Quick Start

### Prerequisites

- Python 3.7+
- AWS account with appropriate credentials
- For Bedrock dashboard: AWS Bedrock logging enabled

### Installation

```bash
# Clone or download the repository
cd dashboards

# Create and activate Python virtual environment
python3 -m venv .venv
source .venv/bin/activate

# Install dependencies
pip install -r requirements.txt
```

### Running Locally

```bash
# Start the Bedrock dashboard on default port 5000
.venv/bin/python bedrock-usage/app.py

# Or specify a custom port
.venv/bin/python bedrock-usage/app.py --port 8080
```

Then open `http://localhost:5000` in your browser.

## Cost Notifications

Set up automated email alerts when your AWS costs reach specific thresholds. Get notified immediately when spending crosses $100, $500, $1000, or any custom amounts you define.

### Quick Setup

```bash
# Setup cost notifications with multiple thresholds
./cost-notification.sh --profile prod --dollar 100,500,1000 --to finance@company.com,devops@company.com
```

### How It Works

The script creates a multi-layered notification system:

1. **AWS Budgets** - Alerts when actual or forecasted monthly spend reaches your thresholds
2. **CloudWatch Billing Alarms** - Backup alerts using AWS's estimated charges metric
3. **SNS Topic** - Central notification hub for all billing alerts
4. **Email Subscriptions** - Sends alerts to specified email addresses

### Features

- **Multiple thresholds** - Set alerts at $100, $250, $500, $1000+ or any amounts
- **Multiple recipients** - Notify finance, devops, and other teams
- **Real-time alerts** - Get notified when thresholds are crossed
- **Automatic sorting** - Thresholds are automatically organized in order
- **Idempotent** - Safe to run multiple times; won't create duplicate alerts

### Usage

```bash
./cost-notification.sh \
  --profile <aws-profile> \
  --dollar <threshold1,threshold2,threshold3> \
  --to <email1,email2>
```

**Parameters:**
- `--profile`: AWS CLI profile to use (default: environment variable `AWS_PROFILE` or `default`)
- `--dollar`: Comma-separated cost thresholds in USD (e.g., `100,250.50,500,1000`)
- `--to`: Comma-separated email addresses for notifications

**Examples:**

```bash
# Single threshold, single email
./cost-notification.sh --profile prod --dollar 100 --to admin@example.com

# Multiple thresholds, multiple recipients
./cost-notification.sh --profile prod --dollar 50,100,250,500,1000 \
  --to finance@company.com,devops@company.com,cto@company.com

# Using decimal amounts with environment variable
AWS_PROFILE=prod ./cost-notification.sh --dollar 99.99,249.99,499.99 --to billing@company.com
```

### Important: Email Confirmation

After running the script:

1. Check your email inboxes for **AWS SNS Subscription Confirmation** messages
2. Click the confirmation link in each email
3. **Without confirmation, you will NOT receive alerts**

The confirmation ensures only authorized recipients receive notifications.

### Managing Notifications

**View SNS topic subscriptions:**
```bash
aws sns list-subscriptions-by-topic --topic-arn <topic-arn> --profile prod
```

**View AWS Budgets:**
```bash
aws budgets describe-budgets --account-id <account-id> --profile prod
```

**View CloudWatch alarms:**
```bash
aws cloudwatch describe-alarms --profile prod
```

**Delete a budget** (to remove a threshold):
```bash
aws budgets delete-budget \
  --account-id <account-id> \
  --budget-name cost-alert-100usd-<account-id> \
  --profile prod
```

**Delete a CloudWatch alarm:**
```bash
aws cloudwatch delete-alarms --alarm-names billing-alert-100usd --profile prod
```

## Deployment Options

### Option 1: User Systemd Service (Recommended for VPS/Servers)

Install as a systemd service that runs as your user (auto-starts on login):

```bash
# Install the service
./install-systemd.sh bedrock-usage 5000

# Manage the service
systemctl --user status bedrock-usage        # Check status
journalctl --user -u bedrock-usage -f       # View live logs
systemctl --user restart bedrock-usage      # Restart service

# For service to run when logged out, enable linger
loginctl enable-linger

# Uninstall when done
./uninstall-systemd.sh bedrock-usage
```

Access the dashboard at `http://localhost:5000`

### Option 2: AWS Lambda with API Gateway (Serverless)

Deploy as a serverless function with automatic scaling, automatic SSL/TLS certificates, and custom domain support:

#### Basic Deployment

Deploy with API Gateway endpoint only (no custom domain):

```bash
./setup-lambda.sh bedrock-usage --profile prod
```

Your dashboard will be available at the generated API Gateway endpoint (e.g., `https://abcd1234.execute-api.us-west-2.amazonaws.com`)

#### Deployment with Custom Domain and HTTPS

Deploy with a custom domain name and automatically provisioned SSL certificate:

```bash
./setup-lambda.sh bedrock-usage \
  --profile prod \
  --fqdn bedrock.example.com
```

The script will:
1. Create an AWS Certificate Manager (ACM) certificate with DNS validation
2. Validate the certificate via Route 53
3. Set up an API Gateway custom domain
4. Configure HTTPS/TLS automatically
5. Create Route 53 alias record pointing to the custom domain

Your dashboard will be available at `https://bedrock.example.com`

#### Settings Preservation

Deployment settings are automatically preserved between redeployments:

- When you redeploy **without** `--subnets-only`, the previous subnet restrictions are preserved
- When you redeploy **without** `--fqdn`, the previous domain configuration is preserved
- Settings are stored locally in `~/.lambda-deployments/{function-name}.metadata` for each deployment

This allows safe redeployment updates without losing your access control or domain configuration:

```bash
# First deployment with settings
./setup-lambda.sh bedrock-usage \
  --profile prod \
  --subnets-only 10.0.0.0/8 \
  --fqdn bedrock.example.com

# Later: Update function code without repeating settings
./setup-lambda.sh bedrock-usage --profile prod
# Subnets and domain are automatically preserved from previous deployment
```

**How it works:**
- Settings are saved to `~/.lambda-deployments/bedrock-usage-api.metadata` after each deployment
- On redeployment, if a flag is not provided on the command line, the previous value is used
- To disable a setting (e.g., remove subnet restrictions), explicitly pass an empty value or modify the metadata file

#### Advanced: Cross-Account Deployments

For deployments where your main AWS profile lacks IAM permissions to create Lambda roles:

```bash
./setup-lambda.sh bedrock-usage \
  --profile main-account \
  --iam-profile admin-account \
  --fqdn bedrock.example.com \
  --subnets-only 10.0.0.0/8,172.16.0.0/12
```

**Parameters:**
- `--profile`: AWS profile for Lambda, API Gateway, and domain operations (requires appropriate permissions)
- `--iam-profile`: (Optional) Admin profile for IAM role creation if `--profile` lacks permissions
- `--fqdn`: (Optional) Fully qualified domain name for Route 53. If omitted, uses API Gateway endpoint only
- `--subnets-only`: (Optional) Comma-separated CIDR blocks to restrict access (e.g., `10.0.0.0/8,192.168.0.0/16`)

**Requirements for custom domain:**
- Route 53 hosted zone must exist for your domain (or parent domain)
- Route 53 zone must be in the same AWS account as `--profile`

#### SSL/HTTPS Configuration

When `--fqdn` is provided, the script automatically:

1. **Creates ACM Certificate** - Requests a free SSL certificate from AWS Certificate Manager
2. **Validates via DNS** - Creates a CNAME validation record in Route 53 (automatic)
3. **Waits for Issuance** - Polls ACM until certificate is issued (typically 5-10 minutes)
4. **Binds to API Gateway** - Creates a custom domain name and binds the certificate
5. **Updates Route 53** - Creates an alias record pointing to the custom domain

The certificate is automatically renewed by AWS.

#### Remove Deployment

```bash
./remove-lambda.sh bedrock-usage app.example.com
```

#### Manage Deployment

**View Lambda function:**
```bash
aws lambda get-function --function-name bedrock-usage-api --profile prod
```

**View logs:**
```bash
aws logs tail /aws/lambda/bedrock-usage-api --follow --profile prod
```

**Update function code:**
```bash
./setup-lambda.sh bedrock-usage --profile prod --fqdn bedrock.example.com
```

**DNS troubleshooting:**
```bash
# Check DNS resolution
nslookup bedrock.example.com
dig bedrock.example.com

# Check certificate status
aws acm describe-certificate --certificate-arn <arn> --region us-west-2 --profile prod
```

## Configuration

Configuration is managed through environment variables in `.env`:

```bash
# Copy the template
cp .env.default .env

# Edit with your settings
nano .env
```

**Key variables:**
- `AWS_PROFILE`: AWS CLI profile to use for credentials
- `SUBNET_ONLY`: (Optional) Restrict access to specific network CIDR block
- `FQDN`: (Optional) Domain name for Lambda deployments

**Dashboard-specific overrides** are supported. For example:
- `AWS_PROFILE_BEDROCK_USAGE`: Override AWS profile for Bedrock dashboard only

See `.env.default` for complete examples.

## AWS Permissions

The dashboards need AWS permissions to function. For the Bedrock dashboard:

- `logs:StartQuery` and `logs:GetQueryResults` - Query CloudWatch Logs
- `logs:DescribeLogStreams` - (Optional) For troubleshooting

To set up AWS permissions and enable Bedrock logging:

```bash
cd bedrock-usage

# Create IAM role for Bedrock logging
AWS_PROFILE=your-admin-profile ./setup-iam-for-bedrock.sh

# Enable Bedrock model invocation logging
AWS_PROFILE=your-bedrock-profile ./enable-bedrock-logging.sh

# Grant PassRole permissions to users
AWS_PROFILE=your-admin-profile ./grant-passrole-permission.sh
```

Or run the complete setup:

```bash
AWS_PROFILE=your-profile ./setup-bedrock-logging.sh
```

## Detailed Documentation

- **CLAUDE.md**: Developer and contributor guide with architecture details
- **bedrock-usage/CLAUDE.md**: Comprehensive Bedrock dashboard documentation including testing procedures and advanced configuration



