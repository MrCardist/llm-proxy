from flask import Flask, render_template_string, jsonify, request
import boto3
from datetime import datetime, timedelta
from collections import defaultdict
import json
import os
import ipaddress
import argparse
import sys
import time
import re
from dotenv import load_dotenv

# Load environment variables from .env file
load_dotenv('.env')

# Get the dashboard name from its directory for config lookups (e.g., bedrock-usage/ → BEDROCK_USAGE)
SCRIPT_NAME = os.path.basename(os.path.dirname(os.path.abspath(__file__))).upper().replace('-', '_')

def get_config(variable_name, default=None):
    """
    Get configuration value with dashboard-specific override support.

    Example:
        get_config('SUBNETS_ONLY') will look for:
        1. SUBNETS_ONLY_BEDROCK_USAGE (if dashboard directory is bedrock-usage/)
        2. SUBNETS_ONLY (global fallback)
        3. default value
    """
    # Try dashboard-specific variable first
    specific_var = f"{variable_name}_{SCRIPT_NAME}"
    if specific_var in os.environ:
        return os.environ[specific_var]

    # Fall back to global variable
    if variable_name in os.environ:
        return os.environ[variable_name]

    # Return default
    return default

def parse_days_parameter(days_param):
    """
    Parse the days parameter, which can be a number or special value like 'mtd' or 'last-month'.

    Returns:
        tuple: (start_date, end_date) as datetime objects
    """
    end_date = datetime.now()

    if days_param == 'mtd':
        # Month to date: from first day of current month to today
        start_date = end_date.replace(day=1)
    elif days_param == 'last-month':
        # Last month: from first day of last month to last day of last month
        first_of_this_month = end_date.replace(day=1)
        last_of_last_month = first_of_this_month - timedelta(days=1)
        start_date = last_of_last_month.replace(day=1)
        end_date = last_of_last_month
    else:
        # Numeric days: convert to start_date
        try:
            days = int(days_param)
        except (ValueError, TypeError):
            days = 7  # Default to 7 days
        start_date = end_date - timedelta(days=days)

    return start_date, end_date

app = Flask(__name__)

# Query cache to avoid repeated CloudWatch Logs Insights queries
_query_cache = {}
_cache_ttl = 600  # 10 minutes cache TTL in seconds

def _get_cache_key(days):
    """Generate cache key based on days parameter (handles numeric and special values)"""
    # Convert special values to consistent keys
    if days == 'mtd':
        cache_date = datetime.now().strftime('%Y-%m')
        return f"bedrock_usage_mtd_{cache_date}"
    elif days == 'last-month':
        last_month = datetime.now() - timedelta(days=datetime.now().day)
        cache_date = last_month.strftime('%Y-%m')
        return f"bedrock_usage_last_month_{cache_date}"
    else:
        return f"bedrock_usage_{days}"

def _get_cached_query_id(days):
    """Get cached query ID if still valid"""
    cache_key = _get_cache_key(days)
    if cache_key in _query_cache:
        entry = _query_cache[cache_key]
        if time.time() - entry['timestamp'] < _cache_ttl:
            return entry
    return None

def _cache_query_id(days, query_id, status):
    """Cache a CloudWatch Logs Insights query ID"""
    cache_key = _get_cache_key(days)
    _query_cache[cache_key] = {
        'query_id': query_id,
        'status': status,
        'timestamp': time.time()
    }

# Bedrock pricing (USD per Million tokens)
# NOTE: These are Cross-Region Inference (CRI) prices on AWS Bedrock
# Format: 'model-id': {'input': price_per_million_input_tokens, 'output': price_per_million_output_tokens}
BEDROCK_PRICING = {
    # Claude Sonnet 4.6
    'anthropic.claude-sonnet-4-6': {'input': 3.0, 'output': 15.0},
    'anthropic.claude-sonnet-4-6[1m]': {'input': 6.0, 'output': 22.5},  # Long context

    # Claude Opus 4.6
    'anthropic.claude-opus-4-6-v1': {'input': 5.0, 'output': 25.0},
    'anthropic.claude-opus-4-6-v1[1m]': {'input': 10.0, 'output': 37.5},  # Long context

    # Claude Opus 4.5
    'anthropic.claude-opus-4-5-20251101-v1:0': {'input': 5.0, 'output': 25.0},

    # Claude Haiku 4.5
    'anthropic.claude-haiku-4-5-20251001-v1:0': {'input': 1.0, 'output': 5.0},

    # Claude Sonnet 4.5
    'anthropic.claude-sonnet-4-5-20250929-v1:0': {'input': 3.0, 'output': 15.0},
    'anthropic.claude-sonnet-4-5-20250929-v1:0[1m]': {'input': 6.0, 'output': 22.5},  # Long context

    # Claude Sonnet 4
    'anthropic.claude-sonnet-4-20250514-v1:0': {'input': 3.0, 'output': 15.0},
    'anthropic.claude-sonnet-4-20250514-v1:0[1m]': {'input': 6.0, 'output': 22.5},  # Long context

    # OpenAI models - Cross-Region Inference
    'openai.gpt-oss-20b-1:0': {'input': 0.07, 'output': 0.3},
    'openai.gpt-oss-120b-1:0': {'input': 0.15, 'output': 0.6},

    # DeepSeek models - Cross-Region Inference
    'deepseek.deepseek-r1': {'input': 1.35, 'output': 5.4},
    'deepseek.r1-v1:0': {'input': 1.35, 'output': 5.4},
    'deepseek.deepseek-v3.1': {'input': 0.58, 'output': 1.68},
    'deepseek.v3-v1:0': {'input': 0.58, 'output': 1.68},

    # Qwen models - Cross-Region Inference
    'qwen.qwen3-coder-30b-a3b': {'input': 0.15, 'output': 0.6},
    'qwen.qwen3-32b': {'input': 0.15, 'output': 0.6},
    'qwen.qwen3-235b-a22b-2507': {'input': 0.22, 'output': 0.88},
    'qwen.qwen3-coder-480b-a35b': {'input': 0.22, 'output': 1.8},

    # Amazon Nova models - Cross-Region Inference
    'amazon.nova-micro-v1:0': {'input': 0.035, 'output': 0.00875},
    'amazon.nova-lite-v1:0': {'input': 0.06, 'output': 0.015},
    'amazon.nova-pro-v1:0': {'input': 0.8, 'output': 0.2},
    'amazon.nova-premier-v1:0': {'input': 2.5, 'output': 0.625},

    # Amazon Titan models
    'amazon.titan-text-express-v1': {'input': 0.2, 'output': 0.6},
    'amazon.titan-text-lite-v1': {'input': 0.15, 'output': 0.2},
    'amazon.titan-tg1-large': {'input': 0.8, 'output': 2.4},

    # Cohere models
    'cohere.command-r-v1:0': {'input': 0.5, 'output': 1.5},
    'cohere.command-r-plus-v1:0': {'input': 3.0, 'output': 15.0},

    # Meta Llama models - Cross-Region Inference
    'meta.llama3-70b-instruct-v1:0': {'input': 0.99, 'output': 0.99},
    'meta.llama3-8b-instruct-v1:0': {'input': 0.3, 'output': 0.6},
    'meta.llama3-1-405b-instruct-v1:0': {'input': 2.65, 'output': 2.65},
    'meta.llama3-1-70b-instruct-v1:0': {'input': 0.99, 'output': 0.99},
    'meta.llama3-1-8b-instruct-v1:0': {'input': 0.22, 'output': 0.22},
    'meta.llama3-2-1b-instruct-v1:0': {'input': 0.1, 'output': 0.1},
    'meta.llama3-2-3b-instruct-v1:0': {'input': 0.15, 'output': 0.15},
    'meta.llama3-2-11b-instruct-v1:0': {'input': 0.35, 'output': 0.35},
    'meta.llama3-2-90b-instruct-v1:0': {'input': 1.2, 'output': 1.2},
    'meta.llama3-3-70b-instruct-v1:0': {'input': 0.99, 'output': 0.99},
    'meta.llama4-maverick-17b-instruct-v1:0': {'input': 0.4, 'output': 0.4},
    'meta.llama4-scout-17b-instruct-v1:0': {'input': 0.4, 'output': 0.4},

    # Mistral models - Cross-Region Inference
    'mistral.mistral-7b-instruct-v0:2': {'input': 0.15, 'output': 0.2},
    'mistral.mixtral-8x7b-instruct-v0:1': {'input': 0.45, 'output': 0.7},
    'mistral.mistral-large-2402-v1:0': {'input': 4.0, 'output': 12.0},
    'mistral.mistral-large-2407-v1:0': {'input': 3.0, 'output': 9.0},
    'mistral.pixtral-large-2502-v1:0': {'input': 3.0, 'output': 9.0},

    # Writer models
    'writer.palmyra-x4-v1:0': {'input': 0.375, 'output': 1.125},
    'writer.palmyra-x5-v1:0': {'input': 2.5, 'output': 7.5},

    # TwelveLabs models
    'twelvelabs.pegasus-1-2-v1:0': {'input': 5.0, 'output': 15.0},

    # Stability AI models (image generation - per image pricing converted to token equivalent)
    'stability.stable-image-inpaint-v1:0': {'input': 0.04, 'output': 0.04},  # $0.04/image

    # Default pricing for unknown models
    'default': {'input': 0.0, 'output': 0.0}
}

INACTIVE_BEDROCK_PRICING = {
    # Anthropic models removed from Bedrock
    'anthropic.claude-3-7-sonnet-20250219-v1:0': {'input': 3.0, 'output': 15.0},
    'anthropic.claude-3-5-sonnet-20240620-v1:0': {'input': 3.0, 'output': 15.0},
    'anthropic.claude-3-5-sonnet-20241022-v2:0': {'input': 3.0, 'output': 15.0},
    'anthropic.claude-3-5-haiku-20241022-v1:0': {'input': 1.0, 'output': 5.0},
    'anthropic.claude-3-haiku-20240307-v1:0': {'input': 0.25, 'output': 1.25},
    'anthropic.claude-3-opus-20240229-v1:0': {'input': 15.0, 'output': 75.0},
    'anthropic.claude-3-sonnet-20240229-v1:0': {'input': 3.0, 'output': 15.0},
    'anthropic.claude-opus-4-20250514-v1:0': {'input': 15.0, 'output': 75.0},
    'anthropic.claude-opus-4-1-20250805-v1:0': {'input': 15.0, 'output': 75.0},

    # Claude 2 models (deprecated)
    'anthropic.claude-v2:1': {'input': 8.0, 'output': 24.0},
    'anthropic.claude-v2': {'input': 8.0, 'output': 24.0},
    'anthropic.claude-instant-v1': {'input': 0.8, 'output': 2.4},

    # Amazon Titan models (deprecated)
    'amazon.titan-embed-text-v1': {'input': 0.1, 'output': 0.0},

    # AI21 models (deprecated)
    'ai21.j2-ultra-v1': {'input': 18.8, 'output': 18.8},
    'ai21.j2-mid-v1': {'input': 12.5, 'output': 12.5},

    # Cohere models (deprecated)
    'cohere.command-text-v14': {'input': 1.5, 'output': 2.0},
    'cohere.command-light-text-v14': {'input': 0.3, 'output': 0.6},

}



def strip_model_prefix(model_id):
    """
    Strip ARN prefix and region prefix from model ID.
    Examples:
        'arn:aws:bedrock:us-west-2:405644541454:inference-profile/us.anthropic.claude-sonnet-4-5-20250929-v1:0'
        -> 'anthropic.claude-sonnet-4-5-20250929-v1:0'

        'us.anthropic.claude-3-5-sonnet-20241022-v1:0'
        -> 'anthropic.claude-3-5-sonnet-20241022-v1:0'

        'global.anthropic.claude-opus-4-1-20250805-v1:0'
        -> 'anthropic.claude-opus-4-1-20250805-v1:0'
    """
    # First, strip ARN prefix (arn:aws:bedrock:region:account:inference-profile/)
    if ':inference-profile/' in model_id:
        model_id = model_id.split(':inference-profile/', 1)[1]

    # Then strip region prefix (us., global., eu., ap.)
    if '.' in model_id:
        parts = model_id.split('.', 1)
        if parts[0] in {'us', 'global', 'eu', 'ap'}:  # Use set for O(1) lookup
            return parts[1]

    return model_id

def get_model_display_name(model_id):
    """
    Generate a clean display name for a model ID.
    Examples:
        'anthropic.claude-sonnet-4-5-20250929-v1:0' -> 'Claude Sonnet 4.5 (std)'
        'anthropic.claude-sonnet-4-5-20250929-v1:0[1m]' -> 'Claude Sonnet 4.5 (1m)'
        'anthropic.claude-3-5-haiku-20241022-v1:0' -> 'Claude 3.5 Haiku'
        'openai.gpt-4-20250101-v1:0' -> 'GPT-4'
        'meta.llama3-70b-instruct-v1:0' -> 'Llama3 70B Instruct'

    Steps:
    1. Check for extended thinking suffix [1m] and preserve it
    2. Remove provider prefix (anthropic., openai., meta., etc.)
    3. Remove date suffix (-20240307 format)
    4. Remove version suffix (-v1:0 or -v2:0) if no date was found
    5. Replace hyphens with spaces
    6. Capitalize each word
    7. Add context window label if present
    """
    # Strip already-cleaned model_id (no region prefix)
    clean_id = model_id

    # Step 1: Check for extended thinking suffix [1m]
    has_extended_thinking = '[1m]' in clean_id
    if has_extended_thinking:
        clean_id = clean_id.replace('[1m]', '')

    # Step 2: Remove provider prefix (anthropic., openai., meta., cohere., etc.)
    if '.' in clean_id:
        parts = clean_id.split('.', 1)
        clean_id = parts[1]

    # Step 3: Remove date suffix (e.g., -20240307, -20251025)
    # Pattern: -20YYMMDD or similar 8-digit date
    clean_id = re.sub(r'-\d{8}.*$', '', clean_id)

    # Step 4: If no date was removed, remove version suffix (-v1:0, -v2:0, -1:0, -2:0)
    if re.search(r'-\d{8}', model_id) is None:
        clean_id = re.sub(r'(-v\d:0|-\d:0)$', '', clean_id)

    # Step 5: Replace hyphens and underscores with spaces
    display_name = clean_id.replace('-', ' ').replace('_', ' ')

    # Step 6: Capitalize each word
    display_name = ' '.join(word.capitalize() for word in display_name.split())

    # Step 7: Add context window label for extended thinking only
    if has_extended_thinking:
        display_name += ' (1m)'

    return display_name

def get_model_pricing(model_id):
    """Get pricing for a specific model"""
    # Strip region prefix if present (e.g., 'us.', 'global.', 'eu.')
    clean_model_id = strip_model_prefix(model_id)

    # Try exact match first
    if clean_model_id in BEDROCK_PRICING:
        return BEDROCK_PRICING[clean_model_id]

    if model_id in BEDROCK_PRICING:
        return BEDROCK_PRICING[model_id]

    # Try partial match (for versioned models)
    for key in BEDROCK_PRICING:
        if key in clean_model_id or clean_model_id in key:
            return BEDROCK_PRICING[key]
        if key in model_id or model_id in key:
            return BEDROCK_PRICING[key]

    # Return default pricing
    print(f"WARNING: No pricing found for model: {model_id} (cleaned: {clean_model_id})")
    return BEDROCK_PRICING['default']

# Load HTML templates from files
template_dir = os.path.dirname(os.path.abspath(__file__))

with open(os.path.join(template_dir, 'bedrock-usage-template.html'), 'r') as f:
    HTML_TEMPLATE = f.read()

with open(os.path.join(template_dir, 'bedrock-usage-template-pricing.html'), 'r') as f:
    PRICING_TEMPLATE = f.read()

with open(os.path.join(template_dir, 'bedrock-usage-more-stats.html'), 'r') as f:
    MORE_STATS_TEMPLATE = f.read()

# Keep MATRIX_TEMPLATE for backward compatibility
MATRIX_TEMPLATE = MORE_STATS_TEMPLATE

# VPN/Subnet access error page template
VPN_ERROR_TEMPLATE = """
<!DOCTYPE html>
<html>
<head>
    <title>VPN Required</title>
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, 'Helvetica Neue', sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            padding: 20px;
        }
        .container {
            background: white;
            padding: 50px 40px;
            border-radius: 16px;
            box-shadow: 0 20px 60px rgba(0,0,0,0.3);
            text-align: center;
            max-width: 600px;
            width: 100%;
            animation: slideUp 0.4s ease-out;
        }
        @keyframes slideUp {
            from {
                opacity: 0;
                transform: translateY(30px);
            }
            to {
                opacity: 1;
                transform: translateY(0);
            }
        }
        .icon {
            font-size: 80px;
            margin-bottom: 25px;
            animation: pulse 2s ease-in-out infinite;
        }
        @keyframes pulse {
            0%, 100% {
                transform: scale(1);
            }
            50% {
                transform: scale(1.05);
            }
        }
        h1 {
            color: #2d3748;
            margin: 0 0 15px 0;
            font-size: 32px;
            font-weight: 700;
        }
        .subtitle {
            color: #718096;
            font-size: 18px;
            margin-bottom: 30px;
            line-height: 1.6;
        }
        .info-box {
            background: linear-gradient(135deg, #f7fafc 0%, #edf2f7 100%);
            border-left: 5px solid #667eea;
            padding: 25px;
            margin: 30px 0;
            text-align: left;
            border-radius: 8px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.05);
        }
        .info-box h2 {
            color: #2d3748;
            font-size: 18px;
            margin-bottom: 15px;
            font-weight: 600;
        }
        .info-box ol {
            margin-left: 20px;
            color: #4a5568;
        }
        .info-box li {
            margin: 10px 0;
            line-height: 1.6;
        }
        .info-box .note {
            margin-top: 15px;
            padding-top: 15px;
            border-top: 1px solid #cbd5e0;
            color: #718096;
            font-size: 14px;
            font-style: italic;
        }
        .details {
            background: #f7fafc;
            border-radius: 8px;
            padding: 20px;
            margin-top: 30px;
        }
        .details h3 {
            color: #4a5568;
            font-size: 14px;
            font-weight: 600;
            margin-bottom: 12px;
            text-transform: uppercase;
            letter-spacing: 0.5px;
        }
        .details .info-row {
            display: flex;
            justify-content: space-between;
            align-items: flex-start;
            margin: 8px 0;
            padding: 8px 0;
        }
        .details .label {
            color: #718096;
            font-size: 14px;
            font-weight: 500;
        }
        .details code {
            background: white;
            padding: 6px 12px;
            border-radius: 6px;
            font-family: 'SF Mono', 'Monaco', 'Inconsolata', 'Courier New', monospace;
            color: #d63384;
            font-size: 13px;
            border: 1px solid #e2e8f0;
            display: inline-block;
            max-width: 100%;
            word-break: break-all;
        }
        .subnets-list {
            text-align: left;
            margin-top: 8px;
        }
        .subnets-list code {
            display: block;
            margin: 5px 0;
        }
        @media (max-width: 640px) {
            .container {
                padding: 40px 25px;
            }
            h1 {
                font-size: 26px;
            }
            .subtitle {
                font-size: 16px;
            }
            .details .info-row {
                flex-direction: column;
                gap: 8px;
            }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="icon">🔒</div>
        <h1>VPN Required</h1>
        <p class="subtitle">This dashboard is only accessible from authorized networks.</p>

        <div class="info-box">
            <h2>🔌 Connect to VPN</h2>
            <ol>
                <li><strong>Connect to your organization's VPN</strong></li>
                <li><strong>Use Full Tunnel mode</strong> (not Split Tunnel) if your VPN offers both options</li>
                <li><strong>Refresh this page</strong> (Ctrl+R or Cmd+R)</li>
                <li>If the issue persists, contact your system administrator</li>
            </ol>
            <div class="note">
                💡 Make sure you're connected to the correct VPN profile for this service
            </div>
        </div>

        <div class="details">
            <h3>Connection Details</h3>
            <div class="info-row">
                <span class="label">Your IP Address:</span>
                <code>{{ client_ip }}</code>
            </div>
        </div>
    </div>
</body>
</html>
"""

# Get configuration from environment (with dashboard-specific overrides)
SUBNETS_ONLY = get_config('SUBNETS_ONLY')
AWS_PROFILE = get_config('AWS_PROFILE')
FQDN = get_config('FQDN')

def check_subnet_access():
    """Middleware to check if client IP is within allowed subnets (comma-separated list)"""
    if not SUBNETS_ONLY:
        # No subnet restriction configured
        return True

    try:
        # Get client IP - Lambda/API Gateway uses X-Forwarded-For header
        client_ip = request.headers.get('X-Forwarded-For', request.remote_addr)
        if client_ip and ',' in client_ip:
            # X-Forwarded-For can be "client, proxy1, proxy2" - take first IP
            client_ip = client_ip.split(',')[0].strip()

        if not client_ip:
            print("WARNING: Unable to determine client IP, denying access")
            return render_template_string(
                VPN_ERROR_TEMPLATE,
                client_ip='unknown',
                allowed_subnets='Unable to determine your IP'
            ), 403

        client_ip_obj = ipaddress.ip_address(client_ip)

        # Parse comma-separated list of subnets and always include localhost
        allowed_subnets = [s.strip() for s in SUBNETS_ONLY.split(',') if s.strip()]

        # Always allow localhost (127.0.0.1/8)
        if '127.0.0.1/8' not in allowed_subnets and '127.0.0.0/8' not in allowed_subnets:
            allowed_subnets.append('127.0.0.1/8')

        # Check if client IP is in any allowed subnet
        for subnet_str in allowed_subnets:
            try:
                allowed_network = ipaddress.ip_network(subnet_str, strict=False)
                if client_ip_obj in allowed_network:
                    return True
            except ValueError as ve:
                print(f"WARNING: Invalid subnet in SUBNETS_ONLY: {subnet_str} - {ve}")
                continue

        # IP is not in any allowed subnet
        return render_template_string(
            VPN_ERROR_TEMPLATE,
            client_ip=client_ip,
            allowed_subnets=', '.join(allowed_subnets)
        ), 403
    except Exception as e:
        print(f"ERROR checking subnet access: {e}")
        return True  # Allow access if there's an error checking

# Old template reference (keeping for fallback)
_OLD_HTML_TEMPLATE = """
<!DOCTYPE html>
<html>
<head>
    <title>AWS Bedrock Usage Dashboard</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        body {
            font-family: Arial, sans-serif;
            margin: 20px;
            background-color: #f5f5f5;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
            background-color: white;
            padding: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h1 {
            color: #232f3e;
            border-bottom: 3px solid #ff9900;
            padding-bottom: 10px;
        }
        .chart-container {
            margin: 30px 0;
            height: 400px;
        }
        .loading {
            text-align: center;
            padding: 40px;
            color: #666;
        }
        .error {
            background-color: #fee;
            border: 1px solid #fcc;
            padding: 15px;
            border-radius: 4px;
            color: #c00;
        }
        .stats {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 20px;
            margin: 20px 0;
        }
        .stat-card {
            background-color: #f8f9fa;
            padding: 20px;
            border-radius: 8px;
            border-left: 4px solid #ff9900;
        }
        .stat-card h3 {
            margin: 0 0 10px 0;
            color: #666;
            font-size: 14px;
        }
        .stat-card .value {
            font-size: 28px;
            font-weight: bold;
            color: #232f3e;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>AWS Bedrock Usage Dashboard</h1>
        <div id="loading" class="loading">Loading data from AWS...</div>
        <div id="error" style="display:none;" class="error"></div>

        <div id="stats" class="stats" style="display:none;"></div>

        <div id="charts" style="display:none;">
            <div class="chart-container">
                <canvas id="userInvocationsChart"></canvas>
            </div>
            <div class="chart-container">
                <canvas id="dailyTrendChart"></canvas>
            </div>
            <div class="chart-container">
                <canvas id="modelUsageChart"></canvas>
            </div>
        </div>
    </div>

    <script>
        async function loadData() {
            try {
                const response = await fetch('/api/usage');
                const data = await response.json();

                if (data.error) {
                    showError(data.error);
                    return;
                }

                document.getElementById('loading').style.display = 'none';
                document.getElementById('charts').style.display = 'block';
                document.getElementById('stats').style.display = 'grid';

                renderStats(data);
                renderUserInvocationsChart(data);
                renderDailyTrendChart(data);
                renderModelUsageChart(data);
            } catch (error) {
                showError('Failed to load data: ' + error.message);
            }
        }

        function showError(message) {
            document.getElementById('loading').style.display = 'none';
            document.getElementById('error').style.display = 'block';
            document.getElementById('error').textContent = message;
        }

        function renderStats(data) {
            const statsDiv = document.getElementById('stats');
            const totalInvocations = Object.values(data.user_invocations).reduce((a, b) => a + b, 0);
            const totalUsers = Object.keys(data.user_invocations).length;
            const totalModels = Object.keys(data.model_usage).length;

            statsDiv.innerHTML = `
                <div class="stat-card">
                    <h3>Total Invocations</h3>
                    <div class="value">${totalInvocations.toLocaleString()}</div>
                </div>
                <div class="stat-card">
                    <h3>Active Users</h3>
                    <div class="value">${totalUsers}</div>
                </div>
                <div class="stat-card">
                    <h3>Models Used</h3>
                    <div class="value">${totalModels}</div>
                </div>
                <div class="stat-card">
                    <h3>Date Range</h3>
                    <div class="value" style="font-size: 16px;">${data.date_range}</div>
                </div>
            `;
        }

        function renderUserInvocationsChart(data) {
            const ctx = document.getElementById('userInvocationsChart').getContext('2d');
            new Chart(ctx, {
                type: 'bar',
                data: {
                    labels: Object.keys(data.user_invocations),
                    datasets: [{
                        label: 'Invocations per User',
                        data: Object.values(data.user_invocations),
                        backgroundColor: '#ff9900',
                        borderColor: '#ec7211',
                        borderWidth: 1
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        title: {
                            display: true,
                            text: 'Bedrock Invocations by IAM User'
                        }
                    },
                    scales: {
                        y: {
                            beginAtZero: true
                        }
                    }
                }
            });
        }

        function renderDailyTrendChart(data) {
            const ctx = document.getElementById('dailyTrendChart').getContext('2d');
            new Chart(ctx, {
                type: 'line',
                data: {
                    labels: Object.keys(data.daily_trend),
                    datasets: [{
                        label: 'Daily Invocations',
                        data: Object.values(data.daily_trend),
                        borderColor: '#232f3e',
                        backgroundColor: 'rgba(35, 47, 62, 0.1)',
                        tension: 0.1,
                        fill: true
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        title: {
                            display: true,
                            text: 'Daily Usage Trend'
                        }
                    },
                    scales: {
                        y: {
                            beginAtZero: true
                        }
                    }
                }
            });
        }

        function renderModelUsageChart(data) {
            const ctx = document.getElementById('modelUsageChart').getContext('2d');
            new Chart(ctx, {
                type: 'doughnut',
                data: {
                    labels: Object.keys(data.model_usage),
                    datasets: [{
                        label: 'Invocations by Model',
                        data: Object.values(data.model_usage),
                        backgroundColor: [
                            '#ff9900',
                            '#232f3e',
                            '#37475a',
                            '#ec7211',
                            '#146eb4',
                            '#3184c2'
                        ]
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        title: {
                            display: true,
                            text: 'Usage by Model'
                        }
                    }
                }
            });
        }

        // Load data on page load
        loadData();
    </script>
</body>
</html>
"""

# User mapping: maps primary username to list of aliases
# All usage from aliases will be aggregated under the primary username
USER_MAP = {
    'peterdir': ['aider', 'dirkcli'],
    # Add more mappings as needed:
    # 'john': ['john-dev', 'john-prod'],
}

def normalize_username(raw_user):
    """
    Normalize username by:
    1. Removing 'bedrock-' prefix if present
    2. Mapping aliases to primary username
    3. Aggregating unidentifiable users under 'Other'
    """
    # Remove 'bedrock-' prefix
    user = raw_user
    if user.startswith('bedrock-'):
        user = user[8:]  # Remove 'bedrock-' (8 characters)

    # Check if this user is a primary key in the map
    if user in USER_MAP:
        return user

    # Check if this user is an alias for another user
    for primary_user, aliases in USER_MAP.items():
        if user in aliases:
            return primary_user

    # If it looks like an ARN or unidentifiable (contains 'arn:' or ':'), aggregate to 'Other'
    if ':' in user or user.startswith('arn'):
        return 'Other'

    # Return the normalized user (identifiable but not in map)
    return user

def get_cost_explorer_costs(start_time, end_time):
    """
    Fetch actual Bedrock costs from AWS Cost Explorer API.

    Returns costs broken down by:
    - Service (model) with daily granularity
    - Total costs for the period

    Args:
        start_time: datetime object for start of period
        end_time: datetime object for end of period

    Returns:
        dict with 'model_costs', 'daily_costs', 'model_daily_costs', 'total_cost'
    """
    try:
        ce_client = boto3.client('ce')

        # Format dates for Cost Explorer API (YYYY-MM-DD)
        start_date = start_time.strftime('%Y-%m-%d')
        # Cost Explorer end date is exclusive, so add 1 day
        end_date = (end_time + timedelta(days=1)).strftime('%Y-%m-%d')

        # Get daily costs by service for Bedrock-related services
        # List all known Bedrock service names from AWS billing
        bedrock_services = [
            'Amazon Bedrock',
            'Claude Sonnet 4.6 (Amazon Bedrock Edition)',
            'Claude Opus 4.6 (Amazon Bedrock Edition)',
            'Claude Opus 4.5 (Amazon Bedrock Edition)',
            'Claude Sonnet 4.5 (Amazon Bedrock Edition)',
            'Claude Haiku 4.5 (Amazon Bedrock Edition)',
            'Claude Opus 4.1 (Amazon Bedrock Edition)',
            'Claude Opus 4 (Amazon Bedrock Edition)',
            'Claude Sonnet 4 (Amazon Bedrock Edition)',
            'Claude 3.7 Sonnet (Amazon Bedrock Edition)',
            'Claude 3.5 Sonnet (Amazon Bedrock Edition)',
            'Claude 3.5 Haiku (Amazon Bedrock Edition)',
            'Claude 3 Opus (Amazon Bedrock Edition)',
            'Claude 3 Sonnet (Amazon Bedrock Edition)',
            'Claude 3 Haiku (Amazon Bedrock Edition)',
        ]

        response = ce_client.get_cost_and_usage(
            TimePeriod={
                'Start': start_date,
                'End': end_date
            },
            Granularity='DAILY',
            Metrics=['UnblendedCost'],
            GroupBy=[
                {'Type': 'DIMENSION', 'Key': 'SERVICE'}
            ],
            Filter={
                'Dimensions': {
                    'Key': 'SERVICE',
                    'Values': bedrock_services
                }
            }
        )

        # Process results
        model_costs = defaultdict(float)
        daily_costs = defaultdict(float)
        model_daily_costs = defaultdict(lambda: defaultdict(float))
        total_cost = 0.0

        # Map AWS service names to cleaner model names
        service_to_model = {
            'Amazon Bedrock': 'Amazon Bedrock (Other)',
            'Claude Sonnet 4.6 (Amazon Bedrock Edition)': 'anthropic.claude-sonnet-4-6',
            'Claude Opus 4.6 (Amazon Bedrock Edition)': 'anthropic.claude-opus-4-6-v1',
            'Claude Opus 4.5 (Amazon Bedrock Edition)': 'anthropic.claude-opus-4-5-20251101-v1:0',
            'Claude Sonnet 4.5 (Amazon Bedrock Edition)': 'anthropic.claude-sonnet-4-5-20250929-v1:0',
            'Claude Haiku 4.5 (Amazon Bedrock Edition)': 'anthropic.claude-haiku-4-5-20251001-v1:0',
            'Claude Opus 4.1 (Amazon Bedrock Edition)': 'anthropic.claude-opus-4-1-20250805-v1:0',
            'Claude Opus 4 (Amazon Bedrock Edition)': 'anthropic.claude-opus-4-20250514-v1:0',
            'Claude Sonnet 4 (Amazon Bedrock Edition)': 'anthropic.claude-sonnet-4-20250514-v1:0',
            'Claude 3.7 Sonnet (Amazon Bedrock Edition)': 'anthropic.claude-3-7-sonnet-20250219-v1:0',
            'Claude 3.5 Sonnet (Amazon Bedrock Edition)': 'anthropic.claude-3-5-sonnet-20241022-v2:0',
            'Claude 3.5 Haiku (Amazon Bedrock Edition)': 'anthropic.claude-3-5-haiku-20241022-v1:0',
            'Claude 3 Opus (Amazon Bedrock Edition)': 'anthropic.claude-3-opus-20240229-v1:0',
            'Claude 3 Sonnet (Amazon Bedrock Edition)': 'anthropic.claude-3-sonnet-20240229-v1:0',
            'Claude 3 Haiku (Amazon Bedrock Edition)': 'anthropic.claude-3-haiku-20240307-v1:0',
        }

        for day_result in response.get('ResultsByTime', []):
            date = day_result['TimePeriod']['Start']

            for group in day_result.get('Groups', []):
                service_name = group['Keys'][0]
                cost = float(group['Metrics']['UnblendedCost']['Amount'])

                if cost > 0:
                    # Map service name to model ID (or use service name if not in map)
                    model_id = service_to_model.get(service_name, service_name)

                    model_costs[model_id] += cost
                    daily_costs[date] += cost
                    model_daily_costs[model_id][date] += cost
                    total_cost += cost

        return {
            'model_costs': dict(model_costs),
            'daily_costs': dict(sorted(daily_costs.items())),
            'model_daily_costs': {m: dict(sorted(d.items())) for m, d in model_daily_costs.items()},
            'total_cost': total_cost,
            'source': 'cost_explorer'
        }

    except Exception as e:
        error_msg = str(e)
        if 'AccessDenied' in error_msg:
            error_msg = f'Cost Explorer access denied. Required permission: ce:GetCostAndUsage. Error: {error_msg}'
        return {
            'error': f'Failed to fetch Cost Explorer data: {error_msg}',
            'model_costs': {},
            'daily_costs': {},
            'model_daily_costs': {},
            'total_cost': 0.0,
            'source': 'cost_explorer'
        }


def get_bedrock_usage(days=7):
    """
    Fetch Bedrock usage data from CloudWatch Logs using Logs Insights queries.

    CloudWatch Logs Insights provides:
    - 10-100x faster aggregation than filtering and processing in Python
    - Server-side aggregation of data
    - Better handling of large datasets

    Args:
        days: Can be a number (e.g., 7, 30, 90), 'mtd' for month-to-date, or 'last-month'
    """
    try:
        logs_client = boto3.client('logs')

        # Parse days parameter to get start and end times
        start_time, end_time = parse_days_parameter(days)

        log_group_name = '/aws/bedrock/modelinvocations'

        # CloudWatch Logs Insights query - aggregates data server-side
        # Much faster than fetching all events and aggregating in Python
        # Include date breakdown for daily cost tracking
        query = """
fields @timestamp, identity.arn, modelId, input.inputTokenCount, input.cacheWriteInputTokenCount, output.outputTokenCount
| stats count() as invocations,
         sum(input.inputTokenCount) as total_input_tokens,
         sum(input.cacheWriteInputTokenCount) as total_cache_write_tokens,
         sum(output.outputTokenCount) as total_output_tokens
    by identity.arn, modelId, datefloor(@timestamp, 1d) as date_day
        """

        try:
            # Start the query
            response = logs_client.start_query(
                logGroupName=log_group_name,
                startTime=int(start_time.timestamp()),
                endTime=int(end_time.timestamp()),
                queryString=query
            )

            query_id = response['queryId']

            # Poll for query completion (max 60 seconds)
            max_wait = 60
            poll_interval = 1
            elapsed = 0

            while elapsed < max_wait:
                result = logs_client.get_query_results(queryId=query_id)
                status = result['status']

                if status == 'Complete':
                    break
                elif status == 'Failed':
                    raise Exception(f"CloudWatch Logs Insights query failed: {result.get('statistics', {})}")
                elif status == 'Cancelled':
                    raise Exception("CloudWatch Logs Insights query was cancelled")

                time.sleep(poll_interval)
                elapsed += poll_interval

            if elapsed >= max_wait:
                raise Exception("CloudWatch Logs Insights query timeout (> 60 seconds)")

            # Process query results
            return _process_logs_insights_results(
                result.get('results', []),
                start_time,
                end_time
            )

        except logs_client.exceptions.ResourceNotFoundException:
            return {
                'error': f'CloudWatch log group "{log_group_name}" not found. Bedrock model invocation logging may not be enabled in your AWS account. Check AWS Console > CloudWatch > Log groups.',
                'user_invocations': {},
                'daily_trend': {},
                'model_usage': {},
                'date_range': f'{start_time.strftime("%Y-%m-%d")} to {end_time.strftime("%Y-%m-%d")}'
            }

    except Exception as e:
        error_msg = str(e)

        # Provide helpful error messages for common issues
        if 'AccessDenied' in error_msg:
            error_msg = f'Access Denied: Your AWS credentials do not have permission to access CloudWatch Logs. Required permissions: logs:StartQuery, logs:GetQueryResults. Error: {error_msg}'
        elif 'ExpiredToken' in error_msg:
            error_msg = 'AWS credentials have expired. Please refresh your credentials.'
        elif 'NoCredentialsError' in error_msg or 'Unable to locate credentials' in error_msg:
            error_msg = 'No AWS credentials found. Configure credentials via AWS CLI (aws configure) or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables.'

        return {
            'error': f'Failed to fetch Bedrock usage data: {error_msg}',
            'user_invocations': {},
            'daily_trend': {},
            'model_usage': {},
            'date_range': 'N/A'
        }

def _process_logs_insights_results(records, start_time, end_time):
    """
    Process aggregated results from CloudWatch Logs Insights query.
    Records are already aggregated by identity.arn, modelId, and date_day.

    Note: This function tracks invocations and tokens from CloudWatch logs.
    Actual costs are fetched separately from AWS Cost Explorer for accuracy.
    """
    # Initialize data structures
    user_invocations = defaultdict(int)
    daily_trend = defaultdict(int)
    model_usage = defaultdict(int)

    # Token tracking (costs come from Cost Explorer, not calculated here)
    user_tokens = defaultdict(lambda: {'input': 0, 'output': 0})
    model_tokens = defaultdict(lambda: {'input': 0, 'output': 0})
    model_invocations = defaultdict(int)

    # Track user's share of invocations per model (for cost allocation)
    user_model_invocations = defaultdict(lambda: defaultdict(int))
    user_daily_invocations = defaultdict(lambda: defaultdict(int))
    user_model_daily_invocations = defaultdict(lambda: defaultdict(lambda: defaultdict(int)))

    total_events = 0

    # Cache for stripped model prefixes to avoid redundant work
    model_prefix_cache = {}

    # Process each aggregated record
    for record in records:
        # Convert record list to dict for easier access
        fields = {field['field']: field['value'] for field in record}

        try:
            # Extract fields - they're already strings from CloudWatch
            user_arn = fields.get('identity.arn', 'Unknown')
            model_id = fields.get('modelId', 'Unknown')
            date_day = fields.get('date_day', 'Unknown')

            # Skip records with Unknown user or model
            if user_arn == 'Unknown' or model_id == 'Unknown':
                continue

            invocations = int(fields.get('invocations', 0))
            input_tokens = int(fields.get('total_input_tokens', 0))
            cache_write_tokens = int(fields.get('total_cache_write_tokens', 0))
            output_tokens = int(fields.get('total_output_tokens', 0))

            # Total input tokens = regular input + cache write
            total_input_tokens = input_tokens + cache_write_tokens
            total_output_tokens = output_tokens

            # Extract user name from ARN (optimized)
            if 'user/' in user_arn:
                raw_user = user_arn.split('user/', 1)[1].split('/')[0]
            elif 'assumed-role/' in user_arn:
                raw_user = user_arn.split('assumed-role/', 1)[1].split('/')[0]
            elif ':root' in user_arn:
                raw_user = 'root'
            else:
                raw_user = user_arn

            # Normalize username (remove bedrock- prefix and apply alias mapping)
            user = normalize_username(raw_user)

            # Strip region prefix for cleaner display (with caching)
            if model_id not in model_prefix_cache:
                model_prefix_cache[model_id] = strip_model_prefix(model_id)
            clean_model_id = model_prefix_cache[model_id]

            # Normalize date format to YYYY-MM-DD (Cost Explorer format)
            # CloudWatch returns dates like '2025-12-02 00:00:00.000'
            normalized_date = date_day.split(' ')[0] if ' ' in date_day else date_day

            # Aggregate invocation data
            user_invocations[user] += invocations
            model_usage[clean_model_id] += invocations
            daily_trend[normalized_date] += invocations
            model_invocations[clean_model_id] += invocations

            # Aggregate tokens
            user_tokens[user]['input'] += total_input_tokens
            user_tokens[user]['output'] += total_output_tokens
            model_tokens[clean_model_id]['input'] += total_input_tokens
            model_tokens[clean_model_id]['output'] += total_output_tokens

            # Track user's share of model invocations (for cost allocation)
            user_model_invocations[user][clean_model_id] += invocations
            user_daily_invocations[user][normalized_date] += invocations
            user_model_daily_invocations[user][clean_model_id][normalized_date] += invocations

            total_events += invocations

        except (KeyError, ValueError, TypeError) as e:
            # Skip malformed records
            continue

    # Check if we found any data
    if not user_invocations:
        return {
            'error': f'No Bedrock invocation logs found in CloudWatch Logs from {start_time.strftime("%Y-%m-%d")} to {end_time.strftime("%Y-%m-%d")}. Make sure Bedrock logging is enabled and you have made some API calls.',
            'user_invocations': {},
            'daily_trend': {},
            'model_usage': {},
            'date_range': f'{start_time.strftime("%Y-%m-%d")} to {end_time.strftime("%Y-%m-%d")}'
        }

    # Filter out "Unknown" entries from all dictionaries
    user_invocations = {u: v for u, v in user_invocations.items() if u != 'Unknown'}
    model_usage = {m: v for m, v in model_usage.items() if m != 'Unknown'}
    user_tokens = {u: v for u, v in user_tokens.items() if u != 'Unknown'}
    model_tokens = {m: v for m, v in model_tokens.items() if m != 'Unknown'}
    model_invocations = {m: v for m, v in model_invocations.items() if m != 'Unknown'}
    user_model_invocations = {u: {m: v for m, v in mi.items() if m != 'Unknown'}
                              for u, mi in user_model_invocations.items() if u != 'Unknown'}

    # Calculate total tokens
    total_input_tokens = sum(t['input'] for t in user_tokens.values())
    total_output_tokens = sum(t['output'] for t in user_tokens.values())

    # Fetch actual costs from Cost Explorer
    ce_costs = get_cost_explorer_costs(start_time, end_time)

    # Allocate costs to users based on their share of model invocations
    user_costs = defaultdict(float)
    model_costs = ce_costs.get('model_costs', {})
    daily_costs = ce_costs.get('daily_costs', {})
    user_model_costs = defaultdict(lambda: defaultdict(float))
    user_daily_costs = defaultdict(lambda: defaultdict(float))
    user_model_daily_costs = defaultdict(lambda: defaultdict(lambda: defaultdict(float)))

    # For each model, allocate cost to users based on their invocation share
    for model_id, total_model_cost in model_costs.items():
        # Get total invocations for this model
        total_model_invocations = model_invocations.get(model_id, 0)

        if total_model_invocations > 0:
            # Allocate cost to each user based on their share of invocations
            for user, user_models in user_model_invocations.items():
                user_model_inv = user_models.get(model_id, 0)
                if user_model_inv > 0:
                    # User's share of this model's cost
                    user_share = user_model_inv / total_model_invocations
                    allocated_cost = total_model_cost * user_share
                    user_costs[user] += allocated_cost
                    user_model_costs[user][model_id] += allocated_cost

    # Allocate daily costs to users based on their daily invocation share
    model_daily_costs = ce_costs.get('model_daily_costs', {})
    for model_id, daily_model_costs in model_daily_costs.items():
        for date, day_cost in daily_model_costs.items():
            # Get total invocations for this model on this day
            total_day_inv = sum(
                user_model_daily_invocations.get(u, {}).get(model_id, {}).get(date, 0)
                for u in user_invocations.keys()
            )

            if total_day_inv > 0:
                for user in user_invocations.keys():
                    user_day_inv = user_model_daily_invocations.get(user, {}).get(model_id, {}).get(date, 0)
                    if user_day_inv > 0:
                        user_share = user_day_inv / total_day_inv
                        allocated_cost = day_cost * user_share
                        user_daily_costs[user][date] += allocated_cost
                        user_model_daily_costs[user][model_id][date] += allocated_cost

    total_cost = ce_costs.get('total_cost', 0.0)

    return {
        'user_invocations': dict(user_invocations),
        'daily_trend': dict(sorted(daily_trend.items())),
        'model_usage': dict(model_usage),
        'date_range': f'{start_time.strftime("%Y-%m-%d")} to {end_time.strftime("%Y-%m-%d")}',
        'total_events': total_events,

        # Token data (from CloudWatch logs)
        'user_tokens': {user: dict(tokens) for user, tokens in user_tokens.items()},
        'model_tokens': {model: dict(tokens) for model, tokens in model_tokens.items()},
        'model_invocations': dict(model_invocations),

        # Cost data (from Cost Explorer, allocated to users by invocation share)
        'user_costs': dict(user_costs),
        'model_costs': dict(model_costs),
        'daily_costs': dict(sorted(daily_costs.items())),

        # Per-user daily costs
        'user_daily_costs': {user: dict(sorted(costs.items())) for user, costs in user_daily_costs.items()},

        # Per-user per-model costs (for cost matrix)
        'user_model_costs': {user: dict(costs) for user, costs in user_model_costs.items()},

        # Per-user per-model per-day costs (for daily chart)
        'user_model_daily_costs': {
            user: {
                model: dict(sorted(daily.items()))
                for model, daily in models.items()
            }
            for user, models in user_model_daily_costs.items()
        },

        # Summary totals
        'total_input_tokens': total_input_tokens,
        'total_output_tokens': total_output_tokens,
        'total_cost': total_cost,

        # Source indicator
        'cost_source': 'AWS Cost Explorer'
    }

@app.route('/')
def index():
    """Serve the dashboard HTML"""
    access_check = check_subnet_access()
    if access_check is not True:
        return access_check
    return render_template_string(HTML_TEMPLATE)

@app.route('/api/usage')
def usage_api():
    """API endpoint to fetch usage data"""
    access_check = check_subnet_access()
    if access_check is not True:
        return access_check
    days = request.args.get('days', '7')  # Keep as string, parse_days_parameter handles conversion
    data = get_bedrock_usage(days)

    # Add model display names for friendly UI display
    if 'model_costs' in data and data['model_costs']:
        data['model_display_names'] = {model: get_model_display_name(model) for model in data['model_costs'].keys()}
    else:
        data['model_display_names'] = {}

    # Filter data for graphs: only include models with >= 25 invocations
    # Tables will use the full unfiltered data
    min_invocations_for_graphs = 25
    model_invocations = data.get('model_invocations', {})

    # Create filtered versions for graphs
    filtered_models = {model for model, count in model_invocations.items() if count >= min_invocations_for_graphs}

    # Add filtered data for graphs (with _for_graphs suffix)
    data['model_costs_for_graphs'] = {m: v for m, v in data.get('model_costs', {}).items() if m in filtered_models}
    data['model_invocations_for_graphs'] = {m: v for m, v in model_invocations.items() if m in filtered_models}
    data['model_tokens_for_graphs'] = {m: v for m, v in data.get('model_tokens', {}).items() if m in filtered_models}
    data['model_usage_for_graphs'] = {m: v for m, v in data.get('model_usage', {}).items() if m in filtered_models}
    data['model_display_names_for_graphs'] = {m: v for m, v in data.get('model_display_names', {}).items() if m in filtered_models}

    # Also filter user_model_costs and user_model_daily_costs for graphs
    data['user_model_costs_for_graphs'] = {
        user: {model: cost for model, cost in models.items() if model in filtered_models}
        for user, models in data.get('user_model_costs', {}).items()
    }

    data['user_model_daily_costs_for_graphs'] = {
        user: {
            model: daily_costs
            for model, daily_costs in models.items() if model in filtered_models
        }
        for user, models in data.get('user_model_daily_costs', {}).items()
    }

    return jsonify(data)

@app.route('/api/cost-matrix')
def cost_matrix_api():
    """
    API endpoint to fetch cost matrix data (user x model in dollars).
    Returns a matrix where rows are users and columns are models,
    with cell values showing the cost in dollars.
    """
    access_check = check_subnet_access()
    if access_check is not True:
        return access_check

    days = request.args.get('days', '7')  # Keep as string, parse_days_parameter handles conversion
    data = get_bedrock_usage(days)

    if 'error' in data:
        return jsonify(data)

    # Extract user_model_costs
    user_model_costs = data.get('user_model_costs', {})

    # Build the matrix
    # First, collect all unique users and models
    all_users = sorted(user_model_costs.keys())
    all_models = sorted(set(model for user_costs in user_model_costs.values() for model in user_costs.keys()))

    # Create model display name mapping
    model_display_names = {model: get_model_display_name(model) for model in all_models}

    # Create matrix structure
    matrix = {
        'users': all_users,
        'models': all_models,
        'model_display_names': model_display_names,  # Display names for models
        'data': [],  # 2D array: [user_index][model_index] = cost
        'user_totals': {},  # Total cost per user
        'model_totals': {}  # Total cost per model
    }

    # Populate the matrix
    for user in all_users:
        row = []
        user_total = 0
        for model in all_models:
            cost = user_model_costs.get(user, {}).get(model, 0)
            row.append(round(cost, 4))  # Round to 4 decimal places
            user_total += cost
        matrix['data'].append(row)
        matrix['user_totals'][user] = round(user_total, 4)

    # Calculate model totals
    for model_idx, model in enumerate(all_models):
        model_total = sum(matrix['data'][user_idx][model_idx] for user_idx in range(len(all_users)))
        matrix['model_totals'][model] = round(model_total, 4)

    matrix['date_range'] = data.get('date_range', '')
    matrix['total_cost'] = data.get('total_cost', 0)

    return jsonify(matrix)

def format_price(price):
    """Format price removing unnecessary trailing zeros"""
    if price >= 1:
        return f"{price:.2f}"
    elif price >= 0.01:
        return f"{price:.4f}".rstrip('0').rstrip('.')
    else:
        return f"{price:.6f}".rstrip('0').rstrip('.')

@app.route('/more-stats')
def more_stats_page():
    """Serve the more stats page with detailed cost analysis"""
    access_check = check_subnet_access()
    if access_check is not True:
        return access_check
    return render_template_string(MORE_STATS_TEMPLATE)

@app.route('/matrix')
def matrix_page():
    """Serve the cost matrix page (backward compatibility redirect)"""
    access_check = check_subnet_access()
    if access_check is not True:
        return access_check
    return render_template_string(MATRIX_TEMPLATE)

@app.route('/pricing')
def pricing_page():
    """Serve the pricing table page"""
    access_check = check_subnet_access()
    if access_check is not True:
        return access_check

    # Vendor icons mapping
    vendor_icons = {
        'anthropic': '🤖',
        'openai': '🔶',
        'deepseek': '🌙',
        'qwen': '🤖',
        'amazon': '🔶',
        'ai21': '📚',
        'cohere': '🌊',
        'meta': '👍'
    }

    # Extract vendor and model info from pricing dictionary
    pricing_data = []
    for model_id, prices in BEDROCK_PRICING.items():
        if model_id == 'default':
            continue

        # Strip region prefix (e.g., "global.", "us.", "eu.", "ap.")
        display_model_id = model_id
        if model_id.startswith(('global.', 'us.', 'eu.', 'ap.')):
            display_model_id = model_id.split('.', 1)[1]

        # Extract vendor from model ID
        if '.' in display_model_id:
            vendor_raw = display_model_id.split('.')[0].lower()
        else:
            vendor_raw = 'unknown'

        # Get vendor display name and icon
        vendor_display = vendor_raw.title()
        vendor_icon = vendor_icons.get(vendor_raw, '📦')

        input_price = prices['input']
        output_price = prices['output']
        total_price = input_price + output_price

        pricing_data.append({
            'vendor': vendor_display,
            'vendor_icon': vendor_icon,
            'model_id': display_model_id,
            'input_price': format_price(input_price),
            'output_price': format_price(output_price),
            'total_price': format_price(total_price),
            'input_price_raw': input_price,  # For JavaScript sorting
            'output_price_raw': output_price,
            'total_price_raw': total_price
        })

    # Sort by input price (most expensive first)
    pricing_data.sort(key=lambda x: x['input_price_raw'], reverse=True)

    return render_template_string(PRICING_TEMPLATE, pricing_data=pricing_data)

if __name__ == '__main__':
    # Parse command-line arguments
    # Port priority: command-line arg > PORT env var > default 5000
    default_port = int(os.environ.get('PORT', 5000))
    parser = argparse.ArgumentParser(description='AWS Bedrock Usage Dashboard')
    parser.add_argument('--port', type=int, default=default_port, help=f'Port to run the server on (default: {default_port})')
    args = parser.parse_args()

    print("Starting Bedrock Usage Dashboard...")
    print(f"Open http://localhost:{args.port} in your browser")
    app.run(debug=True, host='0.0.0.0', port=args.port)
