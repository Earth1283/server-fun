mc-stress
=========

A Go CLI tool for stress-testing a Minecraft server by exploiting the
Generational Garbage-First Garbage Collector (G1GC). It executes an
**application-layer Slowloris** attack: holding thousands of half-open
connections whose heap-resident objects survive repeated Minor GCs, get
promoted into the Old Generation, and ultimately trigger a catastrophic
Full GC pause, OOM error, and heap dump.

Tested against vanilla Paper/Spigot servers running on AMD EPYC hardware
with high heap allocations (``-Xms16G -Xmx16G`` or higher).

----

How It Works
------------

The attack proceeds in four phases:

1. **Connect & Bloat**
   Each worker opens a TCP connection and sends a valid Minecraft Handshake
   packet (protocol 764 / 1.20.1). The ``serverAddress`` field — normally a
   hostname — is replaced with a randomly generated string of up to 255
   characters (the protocol maximum). Netty deserialises this into a live
   ``java.lang.String`` object on the JVM heap.

2. **Hold Hostage**
   The client does *not* proceed with the login sequence. Netty keeps a
   ``ChannelHandlerContext`` reference to each session, which in turn keeps
   the bloated ``String`` reachable. G1GC cannot collect it during Minor GCs
   because it is still live from the server's perspective.

3. **The Dribble**
   A single ``0x00`` byte is written to the socket every ``--dribble-interval``
   seconds. This resets Netty's ``ReadTimeoutHandler`` counter, preventing the
   server from closing idle connections before the heap is saturated.

4. **Premature Promotion**
   Across thousands of connections the Eden and Survivor spaces fill with
   surviving objects. G1GC copies them into the Old Generation. Once the Old
   Generation is saturated a Full GC stall occurs; if the heap cannot be
   reclaimed the JVM raises ``OutOfMemoryError`` and (if configured) writes a
   heap dump.

----

Architecture
------------

The tool is written for minimal overhead on the attacker side so that the
bottleneck stays on the target JVM, not the Go process.

Concurrency
~~~~~~~~~~~

``--workers`` goroutines are launched at startup. Each runs an infinite loop::

    for {
        connect → send handshake → hold (dribble loop) → reconnect on drop
    }

There is no shared mutex. Goroutines never coordinate with one another.

Lock-Free Telemetry
~~~~~~~~~~~~~~~~~~~

All statistics use ``sync/atomic`` 64-bit integers:

============== =============================================================
Counter        Meaning
============== =============================================================
``activeConns``  Connections currently in the dribble-hold phase
``newConns``     Connections opened since the last reporter tick (reset each second)
``droppedConns`` Cumulative connections that failed to dial or were kicked
``bytesSent``    Cumulative bytes written (handshakes + dribble bytes)
============== =============================================================

Reporter
~~~~~~~~

A dedicated goroutine wakes every second, reads the atomic counters, swaps
``newConns`` to zero to compute a true per-second rate, and prints a
single-line status::

    [15:04:05] Active:   8432 | New/s:  217 | Dropped:    59 | Sent: 12.40MB

Per-Worker RNG
~~~~~~~~~~~~~~

Each goroutine owns a ``rand.New(rand.NewSource(seed + workerID))`` instance.
Strings are generated without locking and are unique per connection, preventing
the JVM from interning or deduplicating them.

----

Build
-----

Requirements: Go 1.22 or later.

.. code-block:: bash

    git clone <repo>
    cd mc-stress
    go build -o mc-stress .

Or install directly::

    go install mc-stress@latest

----

Usage
-----

.. code-block:: text

    mc-stress <ip:port> [flags]

    Flags:
      -s, --bloat-size int             handshake server-address string length (max 255) (default 255)
      -d, --dribble-interval duration  interval between keep-alive bytes (default 5s)
      -h, --help                       help for mc-stress
      -v, --verbose                    print per-connection TCP errors
      -w, --workers int                concurrent connections to maintain (default 10000)

Examples
~~~~~~~~

Default run against a local server::

    ./mc-stress 127.0.0.1:25565

Push harder — 50 000 workers, faster dribble, verbose errors::

    ./mc-stress 192.168.1.10:25565 -w 50000 -d 3s -v

Reduced bloat to observe partial heap pressure::

    ./mc-stress 127.0.0.1:25565 -w 10000 -s 128 -d 10s

----

OS Tuning (Attacker Side)
-------------------------

At 10 000+ simultaneous connections the default OS limits will be the first
bottleneck. Apply these before running:

**File descriptor limit** (per-process and system-wide)::

    ulimit -n 131072
    sysctl -w fs.file-max=2097152

**Ephemeral port range** — each outbound TCP connection consumes one local port::

    sysctl -w net.ipv4.ip_local_port_range="1024 65535"

**TCP TIME_WAIT recycling** — allows faster port reuse after dropped connections::

    sysctl -w net.ipv4.tcp_tw_reuse=1

**Socket buffer sizes** — reduces per-socket kernel memory at high connection counts::

    sysctl -w net.core.rmem_default=4096
    sysctl -w net.core.wmem_default=4096

With ``-v`` the tool will log ``dial: dial tcp ...: connect: cannot assign
requested address`` when ephemeral ports are exhausted, and
``accept: too many open files`` style errors when file descriptors run out.

----

Target Server Configuration (Observing Results)
------------------------------------------------

To make the JVM susceptible and to capture the resulting heap dump, launch
the Minecraft server with::

    java \
      -Xms16G -Xmx16G \
      -XX:+UseG1GC \
      -XX:+HeapDumpOnOutOfMemoryError \
      -XX:HeapDumpPath=/tmp/mc-heap.hprof \
      -XX:+PrintGCDetails \
      -Xlog:gc*:file=/tmp/gc.log:time,uptime,level,tags \
      -jar server.jar nogui

Key flags:

* ``-XX:+HeapDumpOnOutOfMemoryError`` — writes a ``.hprof`` file the moment the
  OOM is raised. Analyse with Eclipse MAT or VisualVM to see the retained
  ``String`` objects from the injected handshakes.
* ``-Xlog:gc*`` — streams GC events to a log file so you can observe Minor GC
  frequency increasing, Survivor promotion spiking, and the eventual Full GC
  stall.

----

Telemetry Interpretation
------------------------

============  ================================================================
Signal        What it means
============  ================================================================
Active rising  Workers are successfully holding connections; heap pressure is
               building on the target.
New/s stable   The target is accepting connections at a steady rate; increase
               ``-w`` or reduce ``-d`` to accelerate promotion.
New/s drops    The target's Netty accept queue is saturating or the attacker
               OS is hitting a port/FD limit; check ``-v`` output.
Dropped spikes  Server is kicking connections (firewall, rate-limit plugin, or
               a GC pause causing Netty timeouts). Reduce ``-d`` to hold
               longer before timeout fires.
============  ================================================================

----

Protocol Reference
------------------

The handshake packet sent by this tool conforms to the `Minecraft Java
Edition protocol <https://wiki.vg/Protocol>`_ handshaking state:

.. list-table::
   :header-rows: 1
   :widths: 20 20 60

   * - Field
     - Type
     - Value
   * - Packet Length
     - VarInt
     - Length of remaining payload
   * - Packet ID
     - VarInt
     - ``0x00``
   * - Protocol Version
     - VarInt
     - ``764`` (1.20.1)
   * - Server Address
     - String
     - Random alphanumeric, ``--bloat-size`` bytes (max 255)
   * - Server Port
     - Unsigned Short
     - Port from ``<ip:port>`` argument
   * - Next State
     - VarInt
     - ``2`` (Login) — triggers deeper server-side session allocation

----

License
-------

For authorised testing of infrastructure you own or have explicit written
permission to test. Do not use against servers you do not control.
