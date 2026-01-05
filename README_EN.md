<div align="center">
  <img src="logo.svg" alt="LumeoFS Logo" width="200"/>
  
  # LumeoFS
  
  **A Highly Available, High-Performance Distributed File Storage System**
  
  [![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://go.dev/)
  [![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
  [![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()
  
  English | [简体中文](README.md)
</div>

---

## 📖 Introduction

LumeoFS (from Latin "Lumeo" meaning "light") is an enterprise-grade distributed file storage system implemented in Go, designed for high availability, data consistency, and fault tolerance.

### 🌟 Core Features

| Feature | Description |
|---------|-------------|
| 🔄 **Multi-Replica Mechanism** | Default 3-replica strategy ensures high data availability |
| 🎯 **Primary-Secondary Architecture** | Primary replica for read/write, secondary replicas for read-only, ensuring data consistency |
| ⚡ **Raft Consensus** | Multi-Master nodes achieve high availability through Raft protocol |
| 📝 **WAL Mechanism** | Write-Ahead Log ensures data persistence |
| 🔐 **Erasure Coding** | Erasure Coding supports data recovery |
| 🕐 **Version Control** | Vector clock + lease mechanism ensures consistency |
| 💓 **Health Detection** | Automatic fault detection and node recovery |
| 🚀 **High Performance** | Data sharding and parallel transmission, supports large file storage |

---

## 🏗️ System Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Client                                │
│              File Upload/Download/Query                  │
└────────────────────┬────────────────────────────────────┘
                     │
         ┌───────────┴───────────┐
         │                       │
         ▼                       ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│  Master-1       │◄──►│  Master-2       │◄──►│  Master-3       │
│  (Leader)       │    │  (Follower)     │    │  (Follower)     │
│  Raft Consensus │    │  Raft Consensus │    │  Raft Consensus │
└────────┬────────┘    └────────┬────────┘    └────────┬────────┘
         │                      │                       │
         └──────────────────────┼───────────────────────┘
                                │
                ┌───────────────┼───────────────┐
                │               │               │
                ▼               ▼               ▼
        ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
        │  DataNode-1  │ │  DataNode-2  │ │  DataNode-3  │
        │  (Primary)   │ │ (Secondary)  │ │ (Secondary)  │
        │  + WAL       │ │  + WAL       │ │  + WAL       │
        └──────────────┘ └──────────────┘ └──────────────┘
```

### Core Components

- **Cluster Master**: Cluster master node, ensures high availability through Raft protocol
- **DataNode**: Data storage node, responsible for actual data storage
- **Client**: Client tool, provides file operation interface

---

## 🚀 Quick Start

### Prerequisites

- Go 1.24 or higher
- Linux/macOS/Windows operating system
- At least 4GB available memory

### Installation and Build

```bash
# Clone the project
git clone https://github.com/yourusername/LumeoFS.git
cd LumeoFS

# Build all components
make build

# Or build individually
make build-cluster-master  # Build cluster master node
make build-datanode        # Build data node
make build-client          # Build client
```

### Starting the Cluster

**1. Start 3-Node Master Cluster**

```bash
# Start Master-1 (port 9000)
./bin/cluster-master -config configs/cluster-master-1.json &

# Start Master-2 (port 9002)
./bin/cluster-master -config configs/cluster-master-2.json &

# Start Master-3 (port 9004)
./bin/cluster-master -config configs/cluster-master-3.json &
```

**2. Start Data Nodes**

```bash
# Start DataNode-1
./bin/datanode -config configs/datanode-1.json &

# Start DataNode-2
./bin/datanode -config configs/datanode-2.json &

# Start DataNode-3
./bin/datanode -config configs/datanode-3.json &
```

**3. Using the Client**

```bash
# Check cluster status
./bin/client status

# Upload file
./bin/client upload test.txt

# Download file
./bin/client download <file-id> output.txt

# Query files
./bin/client list
```

---

## 📚 Documentation

| Document | Description |
|----------|-------------|
| [System Architecture](docs/ARCHITECTURE.md) | Detailed technical architecture, basic concepts, working principles |
| [Deployment Guide](docs/DEPLOYMENT.md) | Production environment deployment configuration and best practices |
| [Operations Manual](docs/OPERATIONS.md) | Daily maintenance, fault recovery, backup and restore |
| [API Documentation](docs/API.md) | Development interface documentation and example code |
| [Performance Optimization](docs/PERFORMANCE.md) | Performance tuning and benchmarking |

---

## 🎯 Project Directory Structure

```
LumeoFS/
├── cmd/                          # Executable program entry
│   ├── cluster-master/           # Cluster master node service
│   ├── datanode/                 # Data node service
│   └── client/                   # Client CLI
├── internal/                     # Internal implementation
│   ├── master/                   # Master node logic
│   │   ├── master.go             # Master node core
│   │   ├── replica_manager.go    # Replica manager
│   │   └── cluster_master.go     # Cluster master node
│   ├── datanode/                 # Data node logic
│   │   └── datanode.go           # Data storage
│   ├── raft/                     # Raft consensus protocol
│   │   └── raft.go               # Raft implementation
│   ├── replication/              # Replica synchronization
│   │   └── replication.go        # Replication mechanism
│   ├── version/                  # Version control
│   │   └── version.go            # Vector clock
│   ├── wal/                      # Write-Ahead Log
│   │   └── wal.go                # WAL implementation
│   └── erasure/                  # Erasure coding
│       └── erasure.go            # EC encoding
├── pkg/                          # Public packages
│   ├── common/                   # Common types
│   └── config/                   # Configuration management
├── configs/                      # Configuration files
│   ├── cluster-master-*.json     # Master node configurations
│   └── datanode.json             # Data node configuration
├── docs/                         # Documentation directory
├── bin/                          # Build output
├── Makefile                      # Build script
├── go.mod                        # Go module
└── README.md                     # This file
```

---

## 🧪 Testing and Verification

```bash
# Run unit tests
make test

# Run integration tests
make integration-test

# Performance benchmark
make benchmark
```

### Verified Features

- ✅ Multi-Master node Raft election
- ✅ Automatic master node fault recovery
- ✅ Data node heartbeat detection
- ✅ File sharding and replica storage
- ✅ Primary-secondary replica data synchronization
- ✅ Vector clock version control
- ✅ Lease mechanism for read/write coordination
- ✅ WAL log persistence
- ✅ Erasure code data recovery

---

## 🔧 Configuration

### Master Node Configuration Example

```json
{
  "node_id": "master-1",
  "address": "0.0.0.0",
  "port": 9000,
  "raft_port": 9100,
  "data_dir": "./data/master-1",
  "replica_count": 3,
  "election_timeout": 3000,
  "heartbeat_interval": 300,
  "peers": [
    {
      "node_id": "master-2",
      "address": "127.0.0.1",
      "port": 9002,
      "raft_port": 9102
    }
  ]
}
```

### DataNode Configuration Example

```json
{
  "node_id": "datanode-1",
  "address": "0.0.0.0",
  "port": 10001,
  "data_dir": "./data/datanode-1",
  "master_address": "127.0.0.1:9000",
  "heartbeat_interval": 3000
}
```

---

## 🛠️ Operations

### Check Cluster Status

```bash
# Check Master node processes
ps aux | grep cluster-master

# Windows environment
tasklist | findstr cluster-master
```

### Node Maintenance

```bash
# Take node offline (planned maintenance)
kill <PID>  # or taskkill //PID <PID> (Windows)

# Bring node online
./bin/cluster-master -config configs/cluster-master-1.json &
```

### Fault Recovery

- **Single Node Failure**: Cluster automatically elects new Leader (~3 seconds)
- **Multiple Node Failure**: Ensure (N+1)/2 nodes are alive for normal service
- **Data Corruption**: Automatic recovery through erasure coding

See [Operations Manual](docs/OPERATIONS.md) for details

---

## 📈 Performance Metrics

| Metric | Performance |
|--------|-------------|
| Write Throughput | ~500 MB/s (3 replicas) |
| Read Throughput | ~1 GB/s (single replica) |
| Write Latency | < 10ms (LAN) |
| Read Latency | < 5ms (LAN) |
| Concurrent Connections | 10000+ |
| File Count | Unlimited (limited by metadata storage) |

---

## 🗺️ Roadmap

- [ ] Cross-datacenter deployment support
- [ ] Data compression
- [ ] S3-compatible interface
- [ ] Data encryption support
- [ ] Web management interface
- [ ] Monitoring and alerting system
- [ ] Auto-scaling
- [ ] Hot/cold data tiering

---

## 🤝 Contributing

Contributions are welcome! Please follow these steps:

1. Fork this project
2. Create a feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

---

## 📄 License

This project is licensed under the Apache License 2.0 License - see the [LICENSE](LICENSE) file for details.

---

## 👥 Contact

- **Project Repository**: https://github.com/yourusername/LumeoFS
- **Issue Tracker**: https://github.com/yourusername/LumeoFS/issues
- **Email**: your.email@example.com

---

<div align="center">
  
  **If this project helps you, please give it a ⭐ Star!**
  
  Made with ❤️ by LumeoFS Team
  
</div>
