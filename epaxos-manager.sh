#!/bin/bash

# EPaxos Cluster Manager - Setup and Deployment Script
# Usage: ./epaxos-manager.sh [setup|start|stop|restart|status|logs|cleanup|validate]

set -e

# Configuration
DOCKER_IMAGE="your-dockerhub-username/epaxos:latest"  # Replace with actual image name
PEERS_FILE="peers.txt"
SSH_KEY_PATH="~/.ssh/your-key.pem"  # Path to SSH private key

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging function
log() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
}

warn() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

# Parse peers.txt to get server information
parse_peers_file() {
    if [ ! -f "$PEERS_FILE" ]; then
        error "Peers file '$PEERS_FILE' not found!"
        exit 1
    fi
    
    local replica_id=$1
    local server_ip=""
    local server_port=""
    
    while IFS= read -r line; do
        # Skip empty lines and comments
        if [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]]; then
            continue
        fi
        
        # Parse line: replica_id server_ip port
        read -r id ip port <<< "$line"
        
        if [ "$id" = "$replica_id" ]; then
            server_ip="$ip"
            server_port="$port"
            break
        fi
    done < "$PEERS_FILE"
    
    if [ -z "$server_ip" ] || [ -z "$server_port" ]; then
        error "Replica ID $replica_id not found in $PEERS_FILE"
        exit 1
    fi
    
    echo "$server_ip:$server_port"
}

# Get SSH user for a server (you can customize this logic)
get_ssh_user() {
    local server_ip=$1
    
    # Default SSH user - you can modify this based on your setup
    if [[ "$server_ip" == "localhost" || "$server_ip" == "127.0.0.1" ]]; then
        echo "$USER"
    else
        # You can customize this based on your server naming convention
        echo "ubuntu"  # Default user - change as needed
    fi
}

# SSH helper function
ssh_exec() {
    local server=$1
    local user=$2
    local command=$3
    
    ssh -i "$SSH_KEY_PATH" -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
        "${user}@${server}" "$command"
}

# Get total number of replicas from peers.txt
get_total_replicas() {
    local count=0
    while IFS= read -r line; do
        if [[ -n "$line" && ! "$line" =~ ^[[:space:]]*# ]]; then
            ((count++))
        fi
    done < "$PEERS_FILE"
    echo $count
}

# Validate peers.txt format
validate_peers_file() {
    log "Validating $PEERS_FILE format..."
    
    if [ ! -f "$PEERS_FILE" ]; then
        error "Peers file '$PEERS_FILE' not found!"
        echo "Please create a $PEERS_FILE file with the following format:"
        echo "  replica_id server_ip port"
        echo ""
        echo "Example:"
        echo "  0 192.168.1.10 8000"
        echo "  1 192.168.1.11 8001"
        echo "  2 192.168.1.12 8002"
        exit 1
    fi
    
    local line_number=0
    local replica_ids=()
    
    while IFS= read -r line; do
        ((line_number++))
        
        # Skip empty lines and comments
        if [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]]; then
            continue
        fi
        
        # Check if line has exactly 3 fields
        local field_count=$(echo "$line" | wc -w)
        if [ "$field_count" -ne 3 ]; then
            error "Invalid format in $PEERS_FILE line $line_number: '$line' (expected 3 fields: replica_id server_ip port)"
            exit 1
        fi
        
        # Parse fields
        read -r id ip port <<< "$line"
        
        # Check if replica ID is numeric
        if ! [[ "$id" =~ ^[0-9]+$ ]]; then
            error "Invalid replica ID in $PEERS_FILE line $line_number: '$id' (must be numeric)"
            exit 1
        fi
        
        # Check if port is numeric
        if ! [[ "$port" =~ ^[0-9]+$ ]]; then
            error "Invalid port in $PEERS_FILE line $line_number: '$port' (must be numeric)"
            exit 1
        fi
        
        # Check for duplicate replica IDs
        if [[ " ${replica_ids[@]} " =~ " ${id} " ]]; then
            error "Duplicate replica ID $id in $PEERS_FILE"
            exit 1
        fi
        
        replica_ids+=("$id")
        
    done < "$PEERS_FILE"
    
    local total_replicas=${#replica_ids[@]}
    log "✓ $PEERS_FILE is valid with $total_replicas replicas"
}

# Test SSH connectivity to all servers
test_ssh_connectivity() {
    log "Testing SSH connectivity to all servers..."
    
    local total_replicas=$(get_total_replicas)
    
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local server_port=$(echo "$server_info" | cut -d: -f2)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        info "Testing connection to replica ${replica_id}: ${server_ip}:${server_port}"
        if ssh_exec "$server_ip" "$ssh_user" "echo 'SSH connection successful'" 2>/dev/null; then
            log "✓ Replica ${replica_id} SSH connection successful"
        else
            error "✗ Failed to connect to replica ${replica_id}: ${server_ip}"
            return 1
        fi
    done
    
    log "All SSH connections successful!"
}

# Check Docker installation on all servers
check_docker_installation() {
    log "Checking Docker installation on all servers..."
    
    local total_replicas=$(get_total_replicas)
    
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        info "Checking Docker on replica ${replica_id}: ${server_ip}"
        if ssh_exec "$server_ip" "$ssh_user" "docker --version" 2>/dev/null; then
            log "✓ Docker installed on replica ${replica_id}"
        else
            error "✗ Docker not installed on replica ${replica_id}: ${server_ip}"
            return 1
        fi
    done
    
    log "Docker is installed on all servers!"
}

# Test Docker image pull
test_docker_image_pull() {
    log "Testing Docker image pull on all servers..."
    
    local total_replicas=$(get_total_replicas)
    
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        info "Testing image pull on replica ${replica_id}: ${server_ip}"
        if ssh_exec "$server_ip" "$ssh_user" "docker pull ${DOCKER_IMAGE}" 2>/dev/null; then
            log "✓ Image pull successful on replica ${replica_id}"
        else
            error "✗ Failed to pull image on replica ${replica_id}: ${server_ip}"
            return 1
        fi
    done
    
    log "Docker image pull successful on all servers!"
}

# Check port availability
check_port_availability() {
    log "Checking port availability on all servers..."
    
    local total_replicas=$(get_total_replicas)
    
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local server_port=$(echo "$server_info" | cut -d: -f2)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        info "Checking port ${server_port} on replica ${replica_id}: ${server_ip}"
        if ssh_exec "$server_ip" "$ssh_user" "netstat -tuln | grep :${server_port}" 2>/dev/null; then
            warn "⚠ Port ${server_port} is already in use on replica ${replica_id}"
        else
            log "✓ Port ${server_port} available on replica ${replica_id}"
        fi
    done
}

# Deploy to a single server
deploy_to_server() {
    local replica_id=$1
    
    # Parse server information from peers.txt
    local server_info=$(parse_peers_file "$replica_id")
    local server_ip=$(echo "$server_info" | cut -d: -f1)
    local server_port=$(echo "$server_info" | cut -d: -f2)
    local ssh_user=$(get_ssh_user "$server_ip")
    
    log "Deploying replica ${replica_id} to ${server_ip}:${server_port}"
    
    # Pull the latest image
    ssh_exec "$server_ip" "$ssh_user" "docker pull ${DOCKER_IMAGE}"
    
    # Stop existing container if running
    ssh_exec "$server_ip" "$ssh_user" "docker stop epaxos-replica-${replica_id} 2>/dev/null || true"
    ssh_exec "$server_ip" "$ssh_user" "docker rm epaxos-replica-${replica_id} 2>/dev/null || true"
    
    # Copy peers.txt to the server
    ssh_exec "$server_ip" "$ssh_user" "mkdir -p /tmp/epaxos"
    scp -i "$SSH_KEY_PATH" -o StrictHostKeyChecking=no "$PEERS_FILE" "${ssh_user}@${server_ip}:/tmp/epaxos/peers.txt"
    
    # Run the container
    ssh_exec "$server_ip" "$ssh_user" "docker run -d \
        --name epaxos-replica-${replica_id} \
        --network host \
        -e NODE_ID=${replica_id} \
        -e PEERS_FILE=/etc/epaxos/peers.txt \
        -v /tmp/epaxos/peers.txt:/etc/epaxos/peers.txt:ro \
        --restart unless-stopped \
        ${DOCKER_IMAGE}"
    
    log "Replica ${replica_id} deployed successfully on ${server_ip}"
}

# Start all replicas
start_replicas() {
    log "Starting EPaxos deployment across all servers defined in $PEERS_FILE..."
    
    local total_replicas=$(get_total_replicas)
    log "Found $total_replicas replicas in $PEERS_FILE"
    
    # Deploy to each replica
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        deploy_to_server "$replica_id"
    done
    
    log "All replicas started successfully!"
}

# Stop all replicas
stop_replicas() {
    log "Stopping all EPaxos replicas..."
    
    local total_replicas=$(get_total_replicas)
    
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        ssh_exec "$server_ip" "$ssh_user" "docker stop epaxos-replica-${replica_id} 2>/dev/null || true"
        log "Stopped replica ${replica_id} on ${server_ip}"
    done
    
    log "All replicas stopped!"
}

# Check status of all replicas
check_status() {
    log "Checking status of all replicas..."
    
    local total_replicas=$(get_total_replicas)
    
    echo "=== EPaxos Cluster Status ==="
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local server_port=$(echo "$server_info" | cut -d: -f2)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        echo "Replica ${replica_id} (${server_ip}:${server_port}):"
        ssh_exec "$server_ip" "$ssh_user" "docker ps --filter name=epaxos-replica-${replica_id} --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'"
        echo
    done
}

# Show logs for a specific replica
show_logs() {
    local replica_id=${1:-0}
    local total_replicas=$(get_total_replicas)
    
    if [ "$replica_id" -lt 0 ] || [ "$replica_id" -ge "$total_replicas" ]; then
        error "Invalid replica ID: ${replica_id}. Must be between 0 and $((total_replicas - 1))"
        exit 1
    fi
    
    local server_info=$(parse_peers_file "$replica_id")
    local server_ip=$(echo "$server_info" | cut -d: -f1)
    local ssh_user=$(get_ssh_user "$server_ip")
    
    log "Showing logs for replica ${replica_id} (${server_ip})..."
    ssh_exec "$server_ip" "$ssh_user" "docker logs -f epaxos-replica-${replica_id}"
}

# Show logs for all replicas
show_all_logs() {
    log "Showing logs for all replicas..."
    
    local total_replicas=$(get_total_replicas)
    
    # Start background processes to show logs from all servers
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        ssh_exec "$server_ip" "$ssh_user" "docker logs -f epaxos-replica-${replica_id}" &
    done
    
    # Wait for all background processes
    wait
}

# Clean up all containers and images
cleanup() {
    log "Cleaning up all EPaxos containers and images..."
    
    local total_replicas=$(get_total_replicas)
    
    for replica_id in $(seq 0 $((total_replicas - 1))); do
        local server_info=$(parse_peers_file "$replica_id")
        local server_ip=$(echo "$server_info" | cut -d: -f1)
        local ssh_user=$(get_ssh_user "$server_ip")
        
        ssh_exec "$server_ip" "$ssh_user" "docker stop epaxos-replica-${replica_id} 2>/dev/null || true"
        ssh_exec "$server_ip" "$ssh_user" "docker rm epaxos-replica-${replica_id} 2>/dev/null || true"
        ssh_exec "$server_ip" "$ssh_user" "docker rmi ${DOCKER_IMAGE} 2>/dev/null || true"
    done
    
    log "Cleanup completed!"
}

# Run complete setup validation
run_setup() {
    echo "=== EPaxos Setup and Validation ==="
    echo
    
    # Check configuration
    log "Checking configuration..."
    if [ "$DOCKER_IMAGE" = "your-dockerhub-username/epaxos:latest" ]; then
        error "Please update DOCKER_IMAGE in this script with your actual image name"
        exit 1
    fi
    
    if [ "$SSH_KEY_PATH" = "~/.ssh/your-key.pem" ]; then
        error "Please update SSH_KEY_PATH in this script with your actual SSH key path"
        exit 1
    fi
    
    log "Configuration validation passed!"
    echo
    
    # Run all validation tests
    validate_peers_file
    echo
    
    test_ssh_connectivity
    echo
    
    check_docker_installation
    echo
    
    test_docker_image_pull
    echo
    
    check_port_availability
    echo
    
    log "Setup completed successfully!"
    echo
    echo "Your environment is ready for deployment!"
    echo "Run './epaxos-manager.sh start' to start the cluster"
}

# Main script logic
case "${1:-}" in
    "setup")
        run_setup
        ;;
    "start")
        validate_peers_file
        start_replicas
        ;;
    "stop")
        validate_peers_file
        stop_replicas
        ;;
    "restart")
        validate_peers_file
        stop_replicas
        sleep 2
        start_replicas
        ;;
    "status")
        validate_peers_file
        check_status
        ;;
    "logs")
        validate_peers_file
        if [ -n "$2" ]; then
            show_logs "$2"
        else
            show_all_logs
        fi
        ;;
    "cleanup")
        validate_peers_file
        cleanup
        ;;
    "validate")
        validate_peers_file
        ;;
    *)
        echo "EPaxos Cluster Manager - Combined Setup and Deployment Script"
        echo ""
        echo "Usage: $0 {setup|start|stop|restart|status|logs|cleanup|validate}"
        echo ""
        echo "Commands:"
        echo "  setup     - Run complete setup validation (SSH, Docker, ports, etc.)"
        echo "  start     - Start all EPaxos replicas across all servers"
        echo "  stop      - Stop all EPaxos replicas"
        echo "  restart   - Restart all EPaxos replicas"
        echo "  status    - Check status of all replicas"
        echo "  logs      - Show logs from all replicas"
        echo "  logs <id> - Show logs from specific replica"
        echo "  cleanup   - Remove all containers and images"
        echo "  validate  - Validate peers.txt format only"
        echo ""
        echo "Configuration:"
        echo "  - Update DOCKER_IMAGE with your Docker Hub image name"
        echo "  - Update SSH_KEY_PATH with your SSH private key path"
        echo "  - Configure servers in $PEERS_FILE (format: replica_id server_ip port)"
        echo "  - Update get_ssh_user() function if you have different SSH users"
        echo ""
        echo "Example $PEERS_FILE:"
        echo "  0 192.168.1.10 8000"
        echo "  1 192.168.1.11 8001"
        echo "  2 192.168.1.12 8002"
        echo ""
        echo "Quick Start:"
        echo "  1. Configure peers.txt with your servers"
        echo "  2. Update DOCKER_IMAGE and SSH_KEY_PATH in this script"
        echo "  3. Run: $0 setup"
        echo "  4. Run: $0 start"
        echo ""
        exit 1
        ;;
esac 