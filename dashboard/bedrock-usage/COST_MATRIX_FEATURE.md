# Cost Matrix Feature

This document describes the new Cost Matrix visualization added to the Bedrock Usage Dashboard.

## Overview

The cost matrix provides a spreadsheet-style view of costs (in USD) broken down by **User × Model**. This helps you quickly identify which users are using which models and how much each combination costs.

## Access Points

- **Direct URL**: `http://localhost:5000/matrix`
- **From Dashboard**: Link available in the main dashboard navigation
- **API Endpoint**: `/api/cost-matrix?days=7` (returns JSON data)

## Features

### Data Presentation
- **Rows**: Individual users (aggregated by USER_MAP configuration)
- **Columns**: Model names (with region prefixes automatically stripped)
- **Cells**: Cost in USD for that user-model combination
- **Totals Row**: Sum of costs for each model
- **Totals Column**: Sum of costs for each user

### Region Prefix Stripping
The matrix automatically strips AWS region prefixes from model IDs for cleaner display:
- `us.anthropic.claude-3-5-sonnet-20241022-v1:0` → `anthropic.claude-3-5-sonnet-20241022-v1:0`
- `global.amazon.nova-pro-v1:0` → `amazon.nova-pro-v1:0`
- `eu.deepseek.deepseek-v3.1` → `deepseek.deepseek-v3.1`

### Color Coding
Cells are color-coded based on cost relative to the maximum cost in the matrix:
- **Red** (#fee): High cost (≥ 50% of max)
- **Yellow** (#ffd): Medium cost (25-50% of max)
- **Green** (#efe): Low cost (< 25% of max)
- **White**: Zero cost

### Date Range Selection
The matrix supports filtering by time period:
- Last 7 days (default)
- Last 30 days
- Last 90 days

### Summary Card
Displays key statistics:
- Total Cost (all users × models)
- Number of Users
- Number of Models

## Implementation Details

### Backend Changes (app.py)

1. **New Helper Function**: `strip_model_prefix(model_id)`
   - Removes regional prefixes (us., global., eu., ap.)
   - Used throughout for consistent model display

2. **Enhanced Data Tracking**: `user_model_costs` dictionary
   - Tracks per-user per-model costs during CloudWatch log processing
   - Included in `/api/usage` response

3. **New API Endpoint**: `/api/cost-matrix`
   - Accepts `days` query parameter (7, 30, or 90)
   - Returns matrix structure with:
     - `users`: List of user names
     - `models`: List of model names (prefix-stripped)
     - `data`: 2D array of costs [user_index][model_index]
     - `user_totals`: Sum per user
     - `model_totals`: Sum per model
     - `date_range`: Display string
     - `total_cost`: Grand total

4. **New Route**: `/matrix`
   - Serves the HTML matrix template
   - Includes VPN/subnet access check

### Frontend Changes (bedrock-usage-matrix.html)

- Interactive table with sticky headers and row labels
- Responsive scrolling for large datasets
- Date range selector with URL parameter support
- Color-coded cost cells with hover tooltips
- Automatic calculation of totals
- Error handling and no-data states

## Usage Examples

### View 7-day cost matrix
```
http://localhost:5000/matrix
```

### View 30-day cost matrix
```
http://localhost:5000/matrix?days=30
```

### Access JSON API directly
```bash
curl http://localhost:5000/api/cost-matrix?days=7 | jq
```

## Data Structure

The `/api/cost-matrix` endpoint returns JSON like:

```json
{
  "users": ["alice", "bob", "charlie"],
  "models": ["anthropic.claude-3-5-sonnet-20241022-v1:0", "amazon.nova-pro-v1:0"],
  "data": [
    [42.50, 5.25],
    [0.00, 100.75],
    [25.00, 0.00]
  ],
  "user_totals": {
    "alice": 47.75,
    "bob": 100.75,
    "charlie": 25.00
  },
  "model_totals": {
    "anthropic.claude-3-5-sonnet-20241022-v1:0": 67.50,
    "amazon.nova-pro-v1:0": 106.00
  },
  "date_range": "2024-11-04 to 2024-11-11",
  "total_cost": 173.50
}
```

## Integration with Existing Features

- Respects `USER_MAP` configuration (user aliases aggregated)
- Uses same `BEDROCK_PRICING` dictionary for cost calculations
- Supports `SUBNET_ONLY` access control
- Works with dashboard-specific configuration overrides

## Performance Considerations

- Matrix is calculated on-demand from CloudWatch data
- For large datasets (many users × many models), the table may be large
- Browser table rendering handles typical scenarios (10-20 users, 10-50 models) smoothly
- Consider the date range parameter to limit data volume

## Future Enhancements

Potential improvements:
- CSV/Excel export functionality
- Row/column sorting options
- Filtering by user or model
- Heatmap visualization instead of/in addition to table
- Drill-down to see invocation counts alongside costs
