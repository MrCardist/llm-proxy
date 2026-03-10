from flask import Flask, render_template_string, jsonify, request
import json
import os
import sys
import time
import argparse
import subprocess
from datetime import datetime, timedelta
from collections import defaultdict
from dotenv import load_dotenv

try:
    import requests as http_requests
    HAS_REQUESTS = True
except ImportError:
    HAS_REQUESTS = False

# Load environment variables
load_dotenv(os.path.join(os.path.dirname(__file__), '../.env'))
load_dotenv(os.path.join(os.path.dirname(__file__), '.env'))

SCRIPT_NAME = os.path.basename(os.path.dirname(os.path.abspath(__file__))).upper().replace('-', '_')

# Providers classified by type
CLOUD_PROVIDERS = {'openai', 'anthropic', 'gemini', 'bedrock'}
ONPREM_PROVIDERS = {'gpt-oss', 'qwen', 'local'}
HYBRID_PROVIDERS = {'cc', 'cc-local', 'multi'}


def get_config(variable_name, default=None):
    specific_var = f"{variable_name}_{SCRIPT_NAME}"
    if specific_var in os.environ:
        return os.environ[specific_var]
    if variable_name in os.environ:
        return os.environ[variable_name]
    return default


def check_subnet_access():
    subnets_only = get_config('SUBNETS_ONLY')
    if not subnets_only:
        return None
    try:
        import ipaddress
        client_ip = request.environ.get('HTTP_X_FORWARDED_FOR', request.remote_addr)
        if client_ip and ',' in client_ip:
            client_ip = client_ip.split(',')[0].strip()
        allowed_cidrs = [cidr.strip() for cidr in subnets_only.split(',') if cidr.strip()]
        allowed_cidrs.append('127.0.0.1/8')
        client_addr = ipaddress.ip_address(client_ip)
        for cidr in allowed_cidrs:
            if client_addr in ipaddress.ip_network(cidr, strict=False):
                return None
        return jsonify({'error': f'Access denied from {client_ip}'}), 403
    except Exception:
        return None


def parse_days_parameter(days_param):
    end_date = datetime.now()
    if days_param == 'mtd':
        start_date = end_date.replace(day=1, hour=0, minute=0, second=0, microsecond=0)
    elif days_param == 'last-month':
        first_of_month = end_date.replace(day=1)
        start_date = (first_of_month - timedelta(days=1)).replace(day=1, hour=0, minute=0, second=0, microsecond=0)
        end_date = first_of_month
    else:
        try:
            days = int(days_param)
            start_date = end_date - timedelta(days=days)
        except (ValueError, TypeError):
            start_date = end_date - timedelta(days=7)
    return start_date, end_date


def get_cost_file_path():
    configured = get_config('LLM_PROXY_COST_FILE')
    if configured:
        return configured
    # Default: look for cost-tracking.jsonl relative to the proxy working directory
    candidates = [
        os.path.join(os.path.dirname(__file__), '../../logs/cost-tracking.jsonl'),
        os.path.join(os.path.dirname(__file__), '../logs/cost-tracking.jsonl'),
        '/home/appmotel/.local/share/appmotel/proxyisaac2/repo/logs/cost-tracking.jsonl',
        './logs/cost-tracking.jsonl',
    ]
    for path in candidates:
        if os.path.exists(os.path.abspath(path)):
            return os.path.abspath(path)
    return None


def get_proxy_url():
    return get_config('LLM_PROXY_URL', 'http://localhost:9002')


def get_proxy_health():
    if not HAS_REQUESTS:
        return {'error': 'requests library not installed', 'providers': {}}
    proxy_url = get_proxy_url()
    try:
        resp = http_requests.get(f"{proxy_url}/health", timeout=5)
        return resp.json()
    except Exception as e:
        return {'error': str(e), 'providers': {}, 'status': 'unreachable'}


def get_proxy_uptime():
    """Try to get proxy uptime from systemd or process info."""
    # Try to find the appmotel service
    service_names = ['appmotel-proxyisaac2', 'appmotel-proxy1', 'llm-proxy']
    for service in service_names:
        try:
            result = subprocess.run(
                ['systemctl', '--user', 'show', service, '--property=ActiveEnterTimestamp', '--no-pager'],
                capture_output=True, text=True, timeout=3
            )
            if result.returncode == 0 and 'ActiveEnterTimestamp=' in result.stdout:
                ts_str = result.stdout.split('ActiveEnterTimestamp=')[1].strip()
                if ts_str and ts_str != 'n/a':
                    try:
                        start_time = datetime.strptime(ts_str, '%a %Y-%m-%d %H:%M:%S %Z')
                        uptime = datetime.now() - start_time
                        hours = int(uptime.total_seconds() // 3600)
                        minutes = int((uptime.total_seconds() % 3600) // 60)
                        return f"{hours}h {minutes}m"
                    except Exception:
                        pass
        except Exception:
            pass
    return 'N/A'


_cache = {}
_cache_ttl = 60  # 60 second cache


def get_cost_records(start_date, end_date):
    """Read and filter cost records from the JSONL file."""
    cache_key = f"records_{start_date.date()}_{end_date.date()}"
    if cache_key in _cache:
        cached_time, cached_data = _cache[cache_key]
        if time.time() - cached_time < _cache_ttl:
            return cached_data

    cost_file = get_cost_file_path()
    records = []

    if not cost_file or not os.path.exists(cost_file):
        _cache[cache_key] = (time.time(), records)
        return records

    try:
        with open(cost_file, 'r') as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    record = json.loads(line)
                    # Parse timestamp
                    ts_str = record.get('timestamp', '')
                    if ts_str:
                        try:
                            ts = datetime.fromisoformat(ts_str.replace('Z', '+00:00'))
                            # Make timezone-naive for comparison
                            if ts.tzinfo is not None:
                                ts = ts.replace(tzinfo=None)
                            record['_ts'] = ts
                            if start_date <= ts <= end_date:
                                records.append(record)
                        except Exception:
                            records.append(record)
                    else:
                        records.append(record)
                except json.JSONDecodeError:
                    continue
    except Exception as e:
        print(f"Error reading cost file: {e}", file=sys.stderr)

    # Sort by timestamp descending
    records.sort(key=lambda r: r.get('_ts', datetime.min), reverse=True)
    _cache[cache_key] = (time.time(), records)
    return records


def get_route_type(provider):
    if provider in CLOUD_PROVIDERS:
        return 'cloud'
    if provider in ONPREM_PROVIDERS:
        return 'on-prem'
    return 'hybrid'


def aggregate_data(records):
    """Aggregate cost records into dashboard metrics."""
    total_requests = len(records)
    total_tokens = sum(r.get('total_tokens', 0) for r in records)
    total_input_tokens = sum(r.get('input_tokens', 0) for r in records)
    total_output_tokens = sum(r.get('output_tokens', 0) for r in records)
    total_cost = sum(r.get('total_cost', 0.0) for r in records)
    streaming_count = sum(1 for r in records if r.get('is_streaming', False))

    avg_cost = total_cost / total_requests if total_requests > 0 else 0

    # By model
    by_model = defaultdict(lambda: {'requests': 0, 'input_tokens': 0, 'output_tokens': 0,
                                     'total_tokens': 0, 'cost': 0.0})
    for r in records:
        model = r.get('model', 'unknown')
        by_model[model]['requests'] += 1
        by_model[model]['input_tokens'] += r.get('input_tokens', 0)
        by_model[model]['output_tokens'] += r.get('output_tokens', 0)
        by_model[model]['total_tokens'] += r.get('total_tokens', 0)
        by_model[model]['cost'] += r.get('total_cost', 0.0)

    # By provider
    by_provider = defaultdict(lambda: {'requests': 0, 'total_tokens': 0, 'cost': 0.0, 'type': 'cloud'})
    for r in records:
        provider = r.get('provider', 'unknown')
        by_provider[provider]['requests'] += 1
        by_provider[provider]['total_tokens'] += r.get('total_tokens', 0)
        by_provider[provider]['cost'] += r.get('total_cost', 0.0)
        by_provider[provider]['type'] = get_route_type(provider)

    # By user
    by_user = defaultdict(lambda: {'requests': 0, 'total_tokens': 0, 'cost': 0.0})
    for r in records:
        user = r.get('user_id') or r.get('ip_address') or 'anonymous'
        by_user[user]['requests'] += 1
        by_user[user]['total_tokens'] += r.get('total_tokens', 0)
        by_user[user]['cost'] += r.get('total_cost', 0.0)

    # Cloud vs on-prem
    routing = {'cloud': {'requests': 0, 'cost': 0.0}, 'on-prem': {'requests': 0, 'cost': 0.0},
               'hybrid': {'requests': 0, 'cost': 0.0}}
    for r in records:
        route_type = get_route_type(r.get('provider', ''))
        routing[route_type]['requests'] += 1
        routing[route_type]['cost'] += r.get('total_cost', 0.0)

    # Daily trends (last 30 days)
    daily = defaultdict(lambda: {'requests': 0, 'total_tokens': 0, 'cost': 0.0})
    for r in records:
        ts = r.get('_ts')
        if ts:
            day = ts.strftime('%Y-%m-%d')
            daily[day]['requests'] += 1
            daily[day]['total_tokens'] += r.get('total_tokens', 0)
            daily[day]['cost'] += r.get('total_cost', 0.0)

    # Sort daily by date
    daily_sorted = {k: v for k, v in sorted(daily.items())}

    # Top models by cost (for token split chart)
    top_models = sorted(by_model.items(), key=lambda x: x[1]['cost'], reverse=True)[:8]

    # Recent requests (top 50)
    recent = []
    for r in records[:50]:
        ts = r.get('_ts')
        recent.append({
            'timestamp': ts.strftime('%Y-%m-%d %H:%M:%S') if ts else '',
            'request_id': r.get('request_id', ''),
            'user_id': r.get('user_id') or r.get('ip_address') or 'anonymous',
            'provider': r.get('provider', ''),
            'model': r.get('model', ''),
            'endpoint': r.get('endpoint', ''),
            'is_streaming': r.get('is_streaming', False),
            'input_tokens': r.get('input_tokens', 0),
            'output_tokens': r.get('output_tokens', 0),
            'total_tokens': r.get('total_tokens', 0),
            'total_cost': round(r.get('total_cost', 0.0), 6),
            'finish_reason': r.get('finish_reason', ''),
            'is_estimate': r.get('is_estimate', False),
        })

    return {
        'overview': {
            'total_requests': total_requests,
            'total_tokens': total_tokens,
            'total_input_tokens': total_input_tokens,
            'total_output_tokens': total_output_tokens,
            'total_cost': round(total_cost, 4),
            'avg_cost_per_request': round(avg_cost, 6),
            'streaming_count': streaming_count,
            'non_streaming_count': total_requests - streaming_count,
            'streaming_pct': round(streaming_count / total_requests * 100, 1) if total_requests > 0 else 0,
        },
        'by_model': {k: {**v, 'cost': round(v['cost'], 6)} for k, v in by_model.items()},
        'by_provider': {k: {**v, 'cost': round(v['cost'], 6)} for k, v in by_provider.items()},
        'by_user': {k: {**v, 'cost': round(v['cost'], 6)} for k, v in by_user.items()},
        'routing': {k: {**v, 'cost': round(v['cost'], 6)} for k, v in routing.items()},
        'daily_trends': daily_sorted,
        'top_models_token_split': [
            {'model': m, 'input_tokens': d['input_tokens'], 'output_tokens': d['output_tokens']}
            for m, d in top_models
        ],
        'recent_requests': recent,
    }


# Flask app
app = Flask(__name__)

with open(os.path.join(os.path.dirname(__file__), 'llm-proxy-template.html'), 'r') as f:
    DASHBOARD_TEMPLATE = f.read()


@app.before_request
def before_request():
    result = check_subnet_access()
    if result:
        return result


@app.route('/')
def index():
    return render_template_string(DASHBOARD_TEMPLATE)


@app.route('/api/data')
def data_api():
    days_param = request.args.get('days', '7')
    start_date, end_date = parse_days_parameter(days_param)
    records = get_cost_records(start_date, end_date)
    data = aggregate_data(records)
    data['uptime'] = get_proxy_uptime()
    data['cost_file'] = get_cost_file_path() or 'not configured'
    data['proxy_url'] = get_proxy_url()
    data['date_range'] = {
        'start': start_date.strftime('%Y-%m-%d'),
        'end': end_date.strftime('%Y-%m-%d'),
        'days': days_param,
    }
    return jsonify(data)


@app.route('/api/health')
def health_api():
    return jsonify(get_proxy_health())


@app.route('/api/recent')
def recent_api():
    days_param = request.args.get('days', '1')
    start_date, end_date = parse_days_parameter(days_param)
    records = get_cost_records(start_date, end_date)
    data = aggregate_data(records)
    return jsonify({'recent_requests': data['recent_requests']})


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description='LLM Proxy Dashboard')
    parser.add_argument('--port', type=int, default=int(get_config('PORT', 5001)),
                        help='Port to listen on (default: 5001)')
    parser.add_argument('--host', default='0.0.0.0', help='Host to bind to')
    args = parser.parse_args()

    print(f"Starting LLM Proxy Dashboard on http://{args.host}:{args.port}")
    print(f"Proxy URL: {get_proxy_url()}")
    cost_file = get_cost_file_path()
    print(f"Cost file: {cost_file or 'not found (no data will be shown until cost tracking is enabled)'}")
    app.run(debug=True, host=args.host, port=args.port)
