#!/usr/bin/env bash

set -euo pipefail

# Usage information
usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Retrieve ECS task logs with interactive or non-interactive modes.

OPTIONS:
    -c, --cluster CLUSTER       ECS cluster name (default: irm-ecs-cluster-07e4168)
    -f, --family FAMILY         Task family name (default: irm-tmp-prod-deployment-hatchet-keyset-init)
    -r, --region REGION         AWS region (default: us-east-1)
    -n, --num-tasks NUM         Number of tasks to fetch (default: 10)
    -s, --status STATUS         Task status: STOPPED or RUNNING (default: STOPPED)
    -i, --interactive           Force interactive mode (requires gum)
    -h, --help                  Show this help message

EXAMPLES:
    # Interactive mode (requires gum)
    $0 --interactive

    # Non-interactive with defaults
    $0

    # Non-interactive with custom values
    $0 --cluster my-cluster --family my-task --region us-west-2

    # Check running tasks
    $0 --status RUNNING
EOF
}

# Default values
CLUSTER="irm-ecs-cluster-07e4168"
TASK_FAMILY="hatchet-tmp-prod-deployment-hatchet-keyset-init"
REGION="us-east-1"
NUM_TASKS=10
TASK_STATUS="STOPPED"
INTERACTIVE=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -c|--cluster)
            CLUSTER="$2"
            shift 2
            ;;
        -f|--family)
            TASK_FAMILY="$2"
            shift 2
            ;;
        -r|--region)
            REGION="$2"
            shift 2
            ;;
        -n|--num-tasks)
            NUM_TASKS="$2"
            shift 2
            ;;
        -s|--status)
            TASK_STATUS="$2"
            shift 2
            ;;
        -i|--interactive)
            INTERACTIVE=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "ERROR: Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Interactive mode
if [ "$INTERACTIVE" = true ]; then
    if ! command -v gum &> /dev/null; then
        echo "ERROR: Interactive mode requires 'gum'. Install with: brew install gum"
        exit 1
    fi

    echo "Select AWS Region:"
    REGION=$(gum choose --selected="$REGION" "us-east-1" "us-west-2" "eu-west-1")

    echo "Fetching ECS clusters..."
    CLUSTERS=$(aws ecs list-clusters --region "$REGION" --query 'clusterArns[*]' --output text 2>/dev/null | tr '\t' '\n' | awk -F'/' '{print $NF}')

    if [ -z "$CLUSTERS" ]; then
        echo "ERROR: No clusters found or permission denied"
        exit 1
    fi

    echo "Select ECS Cluster:"
    CLUSTER=$(echo "$CLUSTERS" | gum filter --placeholder="Type to search...")

    echo "Fetching task families..."
    TASK_FAMILIES=$(aws ecs list-task-definition-families --region "$REGION" --status ACTIVE --query 'families[*]' --output text 2>/dev/null | tr '\t' '\n')

    if [ -z "$TASK_FAMILIES" ]; then
        echo "ERROR: No task families found"
        exit 1
    fi

    echo "Select Task Family:"
    TASK_FAMILY=$(echo "$TASK_FAMILIES" | gum filter --placeholder="Type to search...")

    echo "Select Task Status:"
    TASK_STATUS=$(gum choose --selected="STOPPED" "STOPPED" "RUNNING")
fi

echo ""
echo "=========================================="
echo "ECS Task Logs Retrieval"
echo "=========================================="
echo "Cluster:     $CLUSTER"
echo "Task Family: $TASK_FAMILY"
echo "Region:      $REGION"
echo "Status:      $TASK_STATUS"
echo ""

echo "Fetching $TASK_STATUS tasks..."
TASK_ARNS=$(aws ecs list-tasks \
    --cluster "$CLUSTER" \
    --family "$TASK_FAMILY" \
    --desired-status "$TASK_STATUS" \
    --region "$REGION" \
    --query "taskArns[0:$NUM_TASKS]" \
    --output text 2>&1)

if [ -z "$TASK_ARNS" ] || [[ "$TASK_ARNS" == *"error"* ]] || [[ "$TASK_ARNS" == "None" ]]; then
    echo "ERROR: No tasks found or permission denied"
    if [[ "$TASK_ARNS" == *"error"* ]]; then
        echo "AWS Error: $TASK_ARNS"
    fi
    echo ""
    echo "Troubleshooting:"
    echo "  1. Verify cluster exists: aws ecs describe-clusters --clusters $CLUSTER --region $REGION"
    echo "  2. List all tasks: aws ecs list-tasks --cluster $CLUSTER --region $REGION"
    echo "  3. Check task family exists: aws ecs list-task-definitions --family-prefix $TASK_FAMILY --region $REGION"
    exit 1
fi

TASK_COUNT=$(echo "$TASK_ARNS" | wc -w | tr -d ' ')
echo "Found $TASK_COUNT $TASK_STATUS task(s)"
echo ""

# If multiple tasks, let user select (if interactive mode enabled and gum available)
if [ "$TASK_COUNT" -gt 1 ] && [ "$INTERACTIVE" = true ] && command -v gum &> /dev/null; then
    echo "Select task to view logs:"
    TASK_ARN_LIST=$(echo "$TASK_ARNS" | tr ' ' '\n')
    LATEST_TASK_ARN=$(echo "$TASK_ARN_LIST" | gum choose)
else
    LATEST_TASK_ARN=$(echo "$TASK_ARNS" | awk '{print $1}')
fi

TASK_ID=$(echo "$LATEST_TASK_ARN" | awk -F'/' '{print $NF}')
echo "Selected task ID: $TASK_ID"
echo ""

echo "Fetching task details..."
TASK_DETAILS=$(aws ecs describe-tasks \
    --cluster "$CLUSTER" \
    --tasks "$LATEST_TASK_ARN" \
    --region "$REGION" \
    --output json 2>&1)

# Check if jq can parse the response (if not, it's a CLI error)
if ! echo "$TASK_DETAILS" | jq -e '.tasks[0]' >/dev/null 2>&1; then
    echo "ERROR: Could not describe task (AWS CLI error)"
    echo "$TASK_DETAILS"
    exit 1
fi

echo "=========================================="
echo "Task Details"
echo "=========================================="
echo "$TASK_DETAILS" | jq -r '
.tasks[0] |
"Task ARN:       \(.taskArn)",
"Task ID:        \(.taskArn | split("/")[-1])",
"Status:         \(.lastStatus // "N/A")",
"Started At:     \(.startedAt // "N/A")",
"Stopped At:     \(.stoppedAt // "N/A")",
"Exit Code:      \(.containers[0].exitCode // "N/A")",
"Stopped Reason: \(.stoppedReason // "N/A")",
""
'

echo "Container Details:"
CONTAINER_COUNT=$(echo "$TASK_DETAILS" | jq -r '.tasks[0].containers | length')
echo "$TASK_DETAILS" | jq -r '
.tasks[0].containers[] |
"  - \(.name):",
"      Status:    \(.lastStatus)",
"      Exit Code: \(.exitCode // "N/A")",
"      Reason:    \(.reason // "N/A")"
'
echo ""

# Verify task actually started
TASK_STARTED=$(echo "$TASK_DETAILS" | jq -r '.tasks[0].startedAt // empty')
if [ -z "$TASK_STARTED" ]; then
    echo "WARNING: Task never started. Check task definition and permissions."
    echo ""
fi

echo "=========================================="
echo "Log Configuration"
echo "=========================================="

echo "Extracting log configuration from task definition..."
TASK_DEF_ARN=$(echo "$TASK_DETAILS" | jq -r '.tasks[0].taskDefinitionArn')
TASK_DEF=$(aws ecs describe-task-definition \
    --task-definition "$TASK_DEF_ARN" \
    --region "$REGION" \
    --output json 2>&1)

# Check if jq can parse the response
if ! echo "$TASK_DEF" | jq -e '.taskDefinition' >/dev/null 2>&1; then
    echo "ERROR: Could not describe task definition (AWS CLI error)"
    echo "$TASK_DEF"
    exit 1
fi

# Extract log configuration for each container
CONTAINER_NAMES=$(echo "$TASK_DEF" | jq -r '.taskDefinition.containerDefinitions[].name')
echo "Containers in task definition: $(echo "$CONTAINER_NAMES" | tr '\n' ' ')"
echo ""

# Try each container to find logs
for CONTAINER_NAME in $CONTAINER_NAMES; do
    echo "Checking container: $CONTAINER_NAME"

    LOG_CONFIG=$(echo "$TASK_DEF" | jq -r --arg name "$CONTAINER_NAME" '
        .taskDefinition.containerDefinitions[] |
        select(.name == $name) |
        .logConfiguration // empty
    ')

    if [ -z "$LOG_CONFIG" ]; then
        echo "  No log configuration found"
        continue
    fi

    LOG_DRIVER=$(echo "$LOG_CONFIG" | jq -r '.logDriver // empty')
    LOG_GROUP=$(echo "$LOG_CONFIG" | jq -r '.options["awslogs-group"] // empty')
    LOG_STREAM_PREFIX=$(echo "$LOG_CONFIG" | jq -r '.options["awslogs-stream-prefix"] // empty')
    LOG_REGION=$(echo "$LOG_CONFIG" | jq -r '.options["awslogs-region"] // empty')

    echo "  Log Driver:  $LOG_DRIVER"
    echo "  Log Group:   $LOG_GROUP"
    echo "  Prefix:      $LOG_STREAM_PREFIX"
    echo "  Region:      $LOG_REGION"

    if [ "$LOG_DRIVER" != "awslogs" ] || [ -z "$LOG_GROUP" ]; then
        echo "  Skipping - not using CloudWatch Logs"
        continue
    fi

    # Verify log group exists
    echo "  Verifying log group exists..."
    if ! aws logs describe-log-groups \
        --log-group-name-prefix "$LOG_GROUP" \
        --region "$LOG_REGION" \
        --query "logGroups[?logGroupName=='$LOG_GROUP']" \
        --output text 2>/dev/null | grep -q "$LOG_GROUP"; then
        echo "  WARNING: Log group does not exist: $LOG_GROUP"
        continue
    fi

    echo "  Log group exists"
    echo ""

    echo "  Searching for log streams matching task ID: $TASK_ID"

    # Search for log streams containing the task ID
    MATCHING_STREAMS_OUTPUT=$(aws logs describe-log-streams \
        --log-group-name "$LOG_GROUP" \
        --region "$LOG_REGION" \
        --order-by LastEventTime \
        --descending \
        --max-items 100 \
        --query "logStreams[?contains(logStreamName, \`$TASK_ID\`)].logStreamName" \
        --output text 2>&1)

    # Check for AWS CLI errors (not just the string "error")
    if [[ "$MATCHING_STREAMS_OUTPUT" == *"An error occurred"* ]] || [[ "$MATCHING_STREAMS_OUTPUT" == *"AccessDenied"* ]]; then
        echo "  ERROR: Could not list log streams - $MATCHING_STREAMS_OUTPUT"
        continue
    fi

    MATCHING_STREAMS=$(echo "$MATCHING_STREAMS_OUTPUT" | tr '\t' '\n')

    if [ -z "$MATCHING_STREAMS" ]; then
        echo "  No log streams found matching task ID: $TASK_ID"
        echo ""
        echo "  Expected log stream pattern: $LOG_STREAM_PREFIX/<container-name>/$TASK_ID"
        echo ""
        echo "  Recent log streams in group (showing last 10):"
        aws logs describe-log-streams \
            --log-group-name "$LOG_GROUP" \
            --region "$LOG_REGION" \
            --order-by LastEventTime \
            --descending \
            --max-items 10 \
            --query 'logStreams[*].[logStreamName,lastEventTime]' \
            --output text 2>/dev/null | awk '{print "    " $0}' || echo "    (Could not list streams)"
        echo ""
        continue
    fi

    # Count matches
    STREAM_COUNT=$(echo "$MATCHING_STREAMS" | wc -l | tr -d ' ')
    echo "  Found $STREAM_COUNT matching log stream(s)"

    if [ "$STREAM_COUNT" -eq 1 ]; then
        LOG_STREAM="$MATCHING_STREAMS"
    elif [ "$INTERACTIVE" = true ] && command -v gum &> /dev/null; then
        echo "  Select log stream:"
        LOG_STREAM=$(echo "$MATCHING_STREAMS" | gum choose)
    else
        LOG_STREAM=$(echo "$MATCHING_STREAMS" | head -1)
    fi

    echo "  Selected: $LOG_STREAM"
    echo ""

    echo "=========================================="
    echo "Logs from: $LOG_STREAM"
    echo "=========================================="

    LOG_OUTPUT=$(aws logs get-log-events \
        --log-group-name "$LOG_GROUP" \
        --log-stream-name "$LOG_STREAM" \
        --region "$LOG_REGION" \
        --start-from-head \
        --output json 2>&1)

    # Check if jq can parse the response
    if ! echo "$LOG_OUTPUT" | jq -e '.events' >/dev/null 2>&1; then
        echo "ERROR: Could not retrieve logs (AWS CLI error)"
        echo "$LOG_OUTPUT"
        continue
    fi

    EVENT_COUNT=$(echo "$LOG_OUTPUT" | jq -r '.events | length')

    if [ "$EVENT_COUNT" -eq 0 ]; then
        echo "No log events found in stream (stream exists but is empty)"
        echo ""
        continue
    fi

    echo "Found $EVENT_COUNT log event(s)"
    echo ""

    echo "$LOG_OUTPUT" | jq -r '.events[] | "\(.timestamp | tonumber / 1000 | strftime("%Y-%m-%d %H:%M:%S"))  \(.message)"'
    echo ""
    echo "=========================================="
    echo "End of Logs"
    echo "=========================================="

    # Successfully found and displayed logs
    exit 0
done

echo "ERROR: Could not find logs for task $TASK_ID"
echo ""
echo "Possible reasons:"
echo "  1. Task never started (check 'Started At' in task details above)"
echo "  2. Task started but immediately failed before logging"
echo "  3. Log configuration is missing or incorrect"
echo "  4. CloudWatch Logs permissions are missing"
echo ""
echo "Troubleshooting:"
echo "  1. Check task execution role has CloudWatch Logs permissions"
echo "  2. Verify log group exists: aws logs describe-log-groups --region $REGION"
echo "  3. Check task definition: aws ecs describe-task-definition --task-definition $TASK_DEF_ARN --region $REGION"
exit 1
