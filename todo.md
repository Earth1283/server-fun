# Project Specification: Minecraft G1GC Stress Tester

## 1. Overview
This document outlines the architecture, features, and operational requirements for a custom, high-performance command-line interface (CLI) tool written in Go. The purpose of this tool is to stress-test a Minecraft server running on high-end hardware (e.g., AMD EPYC) by deliberately exploiting the Generational Garbage-First Garbage Collector (G1GC). 

By executing an **Application-Layer Slowloris** attack, the tool will force premature promotion of bloated objects from the Eden space to the Old Generation, ultimately triggering a Full GC pause, an OutOfMemory (OOM) error, and a subsequent Heap Dump.

## 2. The Attack Vector: "Application-Layer Slowloris"
Unlike a standard network flood which is easily dismissed by Netty and rapidly cleaned by G1GC, this tool employs a hostage strategy to exhaust heap memory.

### Phases of Execution
1.  **Connect & Bloat:** Open a TCP connection and send a valid Minecraft Handshake packet. Instead of a standard server address, inject a dynamically generated, randomized String at the maximum allowed length (typically 255 characters). This forces the JVM to allocate a large String object on the heap.
2.  **Hold Hostage:** Do not disconnect or proceed with the login sequence. By keeping the socket open, Netty retains a reference to the session, preventing G1GC from marking the bloated String as dead memory during Minor GCs.
3.  **The Dribble:** To prevent Netty's `ReadTimeoutHandler` from terminating the connection, send a single byte (e.g., a fragment of a Keep-Alive or Ping packet) every 5 seconds.
4.  **Premature Promotion:** As thousands of these connections are held, G1GC will copy the surviving garbage from Eden -> Survivor -> Old Generation. Once the Old Generation is saturated, a catastrophic Full GC is triggered.

## 3. Tool Architecture
The Go application must be strictly optimized to prevent locking bottlenecks and to maximize the host machine's network stack.

* **Concurrency Model:** An infinite Worker Pool pattern. Spin up thousands of goroutines (tuned to the host's capabilities) that operate independently without sharing Mutex locks.
* **Lock-Free Telemetry:** Utilize Go's `sync/atomic` package for all statistical tracking. Workers will atomically increment counters for open connections, bytes sent, and dropped sockets.
* **Decoupled Reporting:** A dedicated `time.Ticker` goroutine will wake up periodically (e.g., every 1 second), read the atomic counters, print the current Requests Per Second (RPS) and Active Connections to the terminal, and reset the rate counters.

## 4. CLI Features & Interface
The tool will be built using the `github.com/spf13/cobra` framework for standard POSIX-compliant flag parsing.

### Required Arguments & Flags
* `target` (Positional): The IP and Port of the target server (e.g., `127.0.0.1:25565`).
* `--workers`, `-w`: Number of concurrent connections to attempt to hold (Default: `10000`).
* `--bloat-size`, `-s`: Size of the randomized string in the Handshake packet (Default: `255`).
* `--dribble-interval`, `-d`: Seconds between keep-alive byte sends (Default: `5s`).
* `--verbose`, `-v`: Output granular TCP errors (useful for detecting when the host OS runs out of ephemeral ports).

## 5. Environmental Prerequisites

To successfully deploy this tool and observe the results, both the attacking OS and the defending Docker container must be heavily modified.

### Attacker OS Tuning (Linux `sysctl` & `ulimit`)
To hold tens of thousands of simultaneous connections, the OS file descriptor limits must be raised.