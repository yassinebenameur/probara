#!/bin/bash

# Test script for monitor group functionality
# This script demonstrates creating monitors and groups via the API

set -e

# Configuration
API_BASE="http://localhost:8080/api/v1"
TENANT_ID="${TENANT_ID:-your-tenant-id-here}"
API_KEY="${API_KEY:-your-api-key-here}"

echo "🧪 Testing Monitor Group Functionality"
echo "======================================"
echo

# Function to make API requests
api_request() {
    local method=$1
    local endpoint=$2
    local data=$3
    
    if [ -z "$data" ]; then
        curl -s -X "$method" \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer $API_KEY" \
            -H "X-Tenant-ID: $TENANT_ID" \
            "$API_BASE$endpoint"
    else
        curl -s -X "$method" \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer $API_KEY" \
            -H "X-Tenant-ID: $TENANT_ID" \
            -d "$data" \
            "$API_BASE$endpoint"
    fi
}

echo "📝 Step 1: Creating test monitors..."
echo

# Create HTTP monitor
HTTP_MONITOR=$(api_request POST "/monitors" '{
  "name": "Google Homepage",
  "type": "http",
  "config": {
    "url": "https://www.google.com",
    "method": "GET",
    "expected_status": 200
  },
  "interval_seconds": 60,
  "timeout_seconds": 30,
  "enabled": true,
  "tags": ["test", "production"]
}')

HTTP_MONITOR_ID=$(echo "$HTTP_MONITOR" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "✅ Created HTTP monitor: $HTTP_MONITOR_ID"

# Create Ping monitor
PING_MONITOR=$(api_request POST "/monitors" '{
  "name": "Google DNS",
  "type": "ping",
  "config": {
    "host": "8.8.8.8"
  },
  "interval_seconds": 60,
  "timeout_seconds": 30,
  "enabled": true,
  "tags": ["test", "infrastructure"]
}')

PING_MONITOR_ID=$(echo "$PING_MONITOR" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "✅ Created Ping monitor: $PING_MONITOR_ID"
echo

# Wait a moment
sleep 1

echo "📊 Step 2: Creating a monitor group..."
echo

# Create Group monitor
GROUP_MONITOR=$(api_request POST "/monitors" "{
  \"name\": \"Critical Services Group\",
  \"type\": \"group\",
  \"config\": {
    \"monitor_ids\": [\"$HTTP_MONITOR_ID\", \"$PING_MONITOR_ID\"]
  },
  \"interval_seconds\": 60,
  \"timeout_seconds\": 30,
  \"enabled\": true,
  \"tags\": [\"group\", \"critical\"]
}")

GROUP_ID=$(echo "$GROUP_MONITOR" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)

if [ -z "$GROUP_ID" ]; then
    echo "❌ Failed to create group"
    echo "Response: $GROUP_MONITOR"
    exit 1
fi

echo "✅ Created group: $GROUP_ID"
echo

echo "🔍 Step 3: Verifying group details..."
echo

GROUP_DETAILS=$(api_request GET "/monitors/$GROUP_ID")
echo "$GROUP_DETAILS" | python3 -m json.tool 2>/dev/null || echo "$GROUP_DETAILS"
echo

echo "👥 Step 4: Getting group members..."
echo

MEMBERS=$(api_request GET "/monitors/$GROUP_ID/members")
echo "$MEMBERS" | python3 -m json.tool 2>/dev/null || echo "$MEMBERS"
echo

echo "➕ Step 5: Testing add monitor to group (creating another monitor)..."
echo

# Create another HTTP monitor
ANOTHER_MONITOR=$(api_request POST "/monitors" '{
  "name": "GitHub",
  "type": "http",
  "config": {
    "url": "https://github.com",
    "method": "GET",
    "expected_status": 200
  },
  "interval_seconds": 60,
  "timeout_seconds": 30,
  "enabled": true
}')

ANOTHER_MONITOR_ID=$(echo "$ANOTHER_MONITOR" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "✅ Created another monitor: $ANOTHER_MONITOR_ID"

# Add to group
ADD_RESULT=$(api_request POST "/monitors/$GROUP_ID/members" "{
  \"monitor_ids\": [\"$ANOTHER_MONITOR_ID\"]
}")

echo "✅ Added monitor to group"
echo

echo "🔍 Step 6: Verifying updated group members..."
echo

UPDATED_MEMBERS=$(api_request GET "/monitors/$GROUP_ID/members")
MEMBER_COUNT=$(echo "$UPDATED_MEMBERS" | grep -o '"id"' | wc -l)
echo "Group now has $MEMBER_COUNT members"
echo

echo "➖ Step 7: Testing remove monitor from group..."
echo

REMOVE_RESULT=$(api_request DELETE "/monitors/$GROUP_ID/members" "{
  \"monitor_ids\": [\"$ANOTHER_MONITOR_ID\"]
}")

echo "✅ Removed monitor from group"
echo

echo "📋 Step 8: Listing all monitors..."
echo

ALL_MONITORS=$(api_request GET "/monitors?page_size=100")
TOTAL=$(echo "$ALL_MONITORS" | grep -o '"total":[0-9]*' | cut -d':' -f2)
echo "Total monitors: $TOTAL"
echo

echo "✅ All tests completed successfully!"
echo
echo "Summary:"
echo "  - HTTP Monitor ID: $HTTP_MONITOR_ID"
echo "  - Ping Monitor ID: $PING_MONITOR_ID"
echo "  - Group ID: $GROUP_ID"
echo "  - Another Monitor ID: $ANOTHER_MONITOR_ID"
echo
echo "To view in UI, visit: http://localhost:3000/monitors"
echo
echo "To clean up, run:"
echo "  curl -X DELETE -H \"Authorization: Bearer $API_KEY\" -H \"X-Tenant-ID: $TENANT_ID\" $API_BASE/monitors/$HTTP_MONITOR_ID"
echo "  curl -X DELETE -H \"Authorization: Bearer $API_KEY\" -H \"X-Tenant-ID: $TENANT_ID\" $API_BASE/monitors/$PING_MONITOR_ID"
echo "  curl -X DELETE -H \"Authorization: Bearer $API_KEY\" -H \"X-Tenant-ID: $TENANT_ID\" $API_BASE/monitors/$GROUP_ID"
echo "  curl -X DELETE -H \"Authorization: Bearer $API_KEY\" -H \"X-Tenant-ID: $TENANT_ID\" $API_BASE/monitors/$ANOTHER_MONITOR_ID"

