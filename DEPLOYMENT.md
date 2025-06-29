# EPaxos Multi-Server Deployment Guide

This guide explains how to deploy EPaxos across multiple servers using the provided deployment scripts.

## Architecture Overview

The deployment consists of:
- **Multiple Servers**: Each server runs one EPaxos replica
- **SSH Access**: All servers are accessed via SSH for deployment
- **Configuration**: All server information is defined in `peers.txt`

## Prerequisites

### 1. Server Requirements
- All servers must have Docker installed
- All servers must have SSH access configured
- All servers must be accessible from your deployment machine
- All servers must be able to communicate with each other on the specified ports

### 2. Network Configuration
- Each replica listens on a specific port defined in `peers.txt`
- All servers must be able to communicate with each other on these ports
- Firewall rules must allow communication between servers

### 3. SSH Configuration
- SSH key-based authentication must be configured
- The same SSH key should work for all servers
- SSH user must have sudo privileges for Docker operations

## Quick Start

### Step 1: Configure peers.txt

Create or update `peers.txt` with your server configuration:

```bash
# Format: replica_id server_ip port
0 192.168.1.10 8000
1 192.168.1.11 8001
2 192.168.1.12 8002
3 192.168.1.13 8003
4 192.168.1.14 8004
5 192.168.1.15 8005
6 192.168.1.16 8006
7 192.168.1.17 8007
8 192.168.1.18 8008
9 192.168.1.19 8009
10 192.168.1.20 8010
11 192.168.1.21 8011
12 192.168.1.22 8012
13 192.168.1.23 8013
14 192.168.1.24 8014
15 192.168.1.25 8015
```

### Step 2: Configure Scripts

Update the configuration variables in the scripts:

1. In `deploy.sh`:
   ```bash
   DOCKER_IMAGE="your-dockerhub-username/epaxos:latest"
   SSH_KEY_PATH="~/.ssh/your-key.pem"
   ```

2. In `setup.sh`:
   ```bash
   DOCKER_IMAGE="your-dockerhub-username/epaxos:latest"
   SSH_KEY_PATH="~/.ssh/your-key.pem"
   ```

3. Update the `get_ssh_user()` function in both scripts if you have different SSH users:
   ```bash
   get_ssh_user() {
       local server_ip=$1
       
       if [[ "$server_ip" == "localhost" || "$server_ip" == "127.0.0.1" ]]; then
           echo "$USER"
       else
           echo "ubuntu"  # Change this to your SSH username
       fi
   }
   ```

### Step 3: Run Setup and Tests

1. Run the setup script to validate your configuration:
   ```bash
   chmod +x setup.sh
   ./setup.sh
   ```

2. Test network connectivity:
   ```bash
   chmod +x test-network.sh
   ./test-network.sh
   ```

### Step 4: Deploy the Cluster

1. Start all replicas:
   ```bash
   chmod +x deploy.sh
   ./deploy.sh start
   ```

2. Check cluster status:
   ```bash
   ./deploy.sh status
   ```

3. View logs:
   ```bash
   ./deploy.sh logs
   ```

## Scripts Overview

### `deploy.sh` - Main Deployment Script

**Commands:**
- `./deploy.sh start` - Start all EPaxos replicas
- `./deploy.sh stop` - Stop all replicas
- `./deploy.sh restart` - Restart all replicas
- `./deploy.sh status` - Check status of all replicas
- `./deploy.sh logs` - Show logs from all replicas
- `./deploy.sh logs <id>` - Show logs from specific replica
- `./deploy.sh cleanup` - Remove all containers and images
- `./deploy.sh validate` - Validate peers.txt format

### `setup.sh` - Setup and Validation Script

**Features:**
- Validates peers.txt format
- Tests SSH connectivity to all servers
- Checks Docker installation
- Tests Docker image pull
- Checks port availability
- Generates network test script

### `test-network.sh` - Network Connectivity Test

**Features:**
- Tests connectivity between all replicas
- Validates network configuration before deployment
- Tests inter-server communication

### `peers.txt` - Server Configuration File

**Format:**
```
replica_id server_ip port
```

**Example:**
```
0 192.168.1.10 8000
1 192.168.1.11 8001
2 192.168.1.12 8002
```

## Detailed Configuration

### peers.txt Format

Each line in `peers.txt` should contain:
1. **Replica ID**: A unique numeric identifier (0, 1, 2, ...)
2. **Server IP**: The IP address or hostname of the server
3. **Port**: The port number the replica will listen on

**Rules:**
- Replica IDs must be unique and sequential starting from 0
- Ports must be unique across all servers
- IP addresses can be the same if running multiple replicas on one server
- Comments start with `#` and are ignored

### SSH User Configuration

The `get_ssh_user()` function determines which SSH user to use for each server. You can customize this based on your setup:

```bash
get_ssh_user() {
    local server_ip=$1
    
    # Example: Different users for different IP ranges
    if [[ "$server_ip" =~ ^192\.168\.1\. ]]; then
        echo "ubuntu"
    elif [[ "$server_ip" =~ ^10\.0\. ]]; then
        echo "centos"
    else
        echo "admin"
    fi
}
```

### Docker Configuration

Each replica runs with the following Docker configuration:

```bash
docker run -d \
    --name epaxos-replica-${replica_id} \
    --network host \
    -e NODE_ID=${replica_id} \
    -e PEERS_FILE=/etc/epaxos/peers.txt \
    -v /tmp/epaxos/peers.txt:/etc/epaxos/peers.txt:ro \
    --restart unless-stopped \
    ${DOCKER_IMAGE}
```

## Monitoring and Troubleshooting

### Check Cluster Status

```bash
./deploy.sh status
```

This shows the status of all containers across all servers.

### View Logs

View logs from all replicas:
```bash
./deploy.sh logs
```

View logs from a specific replica:
```bash
./deploy.sh logs 5  # View logs from replica 5
```

### Validate Configuration

Check if your `peers.txt` is properly formatted:
```bash
./deploy.sh validate
```

### Common Issues and Solutions

#### 1. SSH Connection Failed
- Verify SSH key path and permissions
- Check if SSH key is added to all servers
- Ensure SSH service is running on all servers
- Update the `get_ssh_user()` function with correct usernames

#### 2. Docker Not Installed
- Install Docker on the affected server
- Ensure user has Docker permissions

#### 3. Port Already in Use
- Stop any existing services using the required ports
- Check for existing EPaxos containers
- Verify port assignments in `peers.txt`

#### 4. Network Connectivity Issues
- Verify firewall rules allow communication between servers
- Check if all servers can reach each other on the required ports
- Run `./test-network.sh` to diagnose connectivity issues

#### 5. Docker Image Pull Failed
- Verify Docker Hub credentials if using private repository
- Check internet connectivity on the affected server
- Verify image name and tag in the script configuration

#### 6. Invalid peers.txt Format
- Ensure each line has exactly 3 fields: replica_id server_ip port
- Check that replica IDs are unique and numeric
- Verify ports are numeric and unique
- Run `./deploy.sh validate` to check format

### Cleanup

To completely remove all EPaxos containers and images:

```bash
./deploy.sh cleanup
```

## Security Considerations

### SSH Security
- Use SSH key-based authentication only
- Disable password authentication
- Use non-standard SSH ports if possible
- Regularly rotate SSH keys

### Network Security
- Configure firewalls to allow only necessary ports
- Use VPN or private networks for inter-server communication
- Monitor network traffic for anomalies

### Docker Security
- Run containers with minimal privileges
- Regularly update Docker images
- Scan images for vulnerabilities
- Use private registries for production deployments

## Performance Tuning

### Resource Limits

Add resource limits to the Docker run command in `deploy.sh`:

```bash
--memory=1g --cpus=2 \
```

### Logging Configuration

Configure log rotation and limits:

```bash
--log-driver=json-file \
--log-opt max-size=100m \
--log-opt max-file=3 \
```

### Network Optimization

For better performance, consider:
- Using dedicated network interfaces
- Configuring TCP tuning parameters
- Using high-performance network drivers

## Backup and Recovery

### Backup Configuration

Create a backup of your configuration:

```bash
cp peers.txt peers.txt.backup
```

### Recovery Procedure

1. Stop all replicas: `./deploy.sh stop`
2. Restore `peers.txt` if needed
3. Restart replicas: `./deploy.sh start`
4. Verify cluster health: `./deploy.sh status`

## Example Deployments

### Local Development (Single Machine)

```bash
# peers.txt
0 localhost 8000
1 localhost 8001
2 localhost 8002
```

### Multi-Server Production

```bash
# peers.txt
0 192.168.1.10 8000
1 192.168.1.11 8001
2 192.168.1.12 8002
3 192.168.1.13 8003
4 192.168.1.14 8004
5 192.168.1.15 8005
```

### Cloud Deployment (AWS/GCP/Azure)

```bash
# peers.txt
0 10.0.1.10 8000
1 10.0.1.11 8001
2 10.0.1.12 8002
3 10.0.1.13 8003
4 10.0.1.14 8004
5 10.0.1.15 8005
```

## Support

For issues with the deployment scripts:
1. Check the logs: `./deploy.sh logs`
2. Verify configuration: `./setup.sh`
3. Test network connectivity: `./test-network.sh`
4. Validate peers.txt: `./deploy.sh validate`
5. Check Docker and SSH connectivity manually

For EPaxos-specific issues, refer to the main project documentation. 