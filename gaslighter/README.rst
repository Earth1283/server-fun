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
   packet (protocol 767 / 1.21.1). The ``serverAddress`` field — normally a
   hostname — is replaced with a randomly generated string of up to 255
   characters (the protocol maximum). Netty deserialises this into a live
   ``java.lang.String`` object on the JVM heap.

2. **Login Sequence**
   After the Handshake the client immediately sends a ``Login Start`` packet
   to avoid the server's 30-second login timeout ("Took too long to log in!").
   The server's response determines which hold strategy is used:

   * **Offline-mode server** — server replies with ``Login Success``.
     The client completes the sequence (``Login Acknowledged``, drain
     ``Configuration`` state, ``Acknowledge Configuration``) and enters
     **Play state**.
   * **Online-mode server with credentials** — server replies with
     ``Encryption Request``. The client performs the full RSA + AES/CFB8
     handshake and calls the Mojang session server. On success, the
     connection also reaches **Play state**.
   * **Online-mode server without credentials** — encryption is attempted
     but the Mojang join call is skipped. The server kicks with
     "Failed to verify username!" and the worker falls back to the
     **dribble strategy** (see below).

3. **Hold Hostage**
   In **Play state** the client responds to the server's periodic
   ``Keep Alive`` packets (every ~15 seconds). The connection — and the
   heap objects it keeps alive — is held indefinitely. No dribble is needed.

   When Play state cannot be reached (online-mode without credentials), the
   **dribble strategy** is used instead: on the first tick a three-byte
   VarInt header (``0xFF 0xFF 0x03`` = 65 535) is sent, telling Netty to
   expect a 65 535-byte frame. Subsequent ticks drip single filler bytes into
   that open frame, resetting the ``ReadTimeoutHandler`` every
   ``--dribble-interval`` seconds without ever completing the frame. At the
   default 5-second interval the frame fills after ~91 hours.

4. **Premature Promotion**
   Across thousands of connections the Eden and Survivor spaces fill with
   surviving objects. G1GC copies them into the Old Generation. Once the Old
   Generation is saturated a Full GC stall occurs; if the heap cannot be
   reclaimed the JVM raises ``OutOfMemoryError`` and (if configured) writes a
   heap dump.

----

Pre-Login Spam Mode
-------------------

The ``--prelogin`` flag enables a high-frequency spamming attack targeting the
``AsyncPlayerPreLoginEvent`` on Paper/Spigot servers. Instead of holding
connections open to bloat the heap over time, this mode focuses on saturating
the server's login processing pipeline:

1. **Connect & Handshake**
   The worker opens a TCP connection and sends the Handshake and ``Login Start``
   packets.
2. **Trigger Event**
   The server receives ``Login Start`` and fires the ``AsyncPlayerPreLoginEvent``.
   Plugins (like LuckPerms, Geyser, or anti-bot filters) often hook into this
   event to perform database lookups or complex logic.
3. **Immediate Cycle**
   By default, the worker waits for a server response (like ``Login Success`` or
   ``Encryption Request``) before closing the connection and reconnecting.
   This ensures the event is fully processed on the server side.

**Hit-and-Run (--har)**
When combined with ``--prelogin``, the ``--har`` flag disables waiting for any
server response. The worker sends the initial packets and immediately closes
the socket. This maximizes throughput, allowing a single attacker to trigger
thousands of pre-login events per second, potentially DOSing the server's
authentication threads or backend databases.

----

Encryption Support
------------------

When the server sends an ``Encryption Request`` the client:

1. Generates a random 16-byte **shared secret**.
2. Parses the server's RSA public key (DER-encoded ``SubjectPublicKeyInfo``).
3. RSA-PKCS1v15-encrypts both the shared secret and the server's verify token.
4. If ``--access-token`` and ``--player-uuid`` are provided, calls
   ``sessionserver.mojang.com/session/minecraft/join`` with the signed SHA1
   server hash (Minecraft's convention: signed big-endian hex).
5. Sends the ``Encryption Response``.
6. Wraps the TCP connection in a custom **AES/CFB8** cipher stream (Go's
   standard library provides CFB128; Minecraft uses 8-bit feedback mode, so
   the implementation is hand-rolled).

All subsequent I/O — including Configuration and Play state packets — passes
through the cipher transparently. Compression (``Set Compression``) is also
handled: the extra ``Data Length`` VarInt is stripped for packets below the
threshold.

----

Architecture
------------

Concurrency
~~~~~~~~~~~

``--workers`` goroutines are launched at startup. Each runs an infinite loop::

    for {
        [wait for join gate] → connect → handshake → login →
        (play state → keep-alive loop) OR (dribble loop) → reconnect on drop
    }

There is no shared mutex. Goroutines never coordinate except through the
optional join gate channel (see ``--join-delay``).

Join Gate
~~~~~~~~~

When ``--join-delay`` is set, a single ``chan struct{}`` with capacity 1 acts
as a global rate limiter. A background goroutine pushes one token per interval;
every worker blocks on a receive before dialling. This limits new connections
to at most one per interval across all workers combined, bypassing server-side
connection throttling.

Lock-Free Telemetry
~~~~~~~~~~~~~~~~~~~

All statistics use ``sync/atomic`` 64-bit integers:

============== =============================================================
Counter        Meaning
============== =============================================================
``activeConns``  Connections currently in the hold phase (Play or dribble)
``newConns``     Connections opened since the last reporter tick (reset each second)
``droppedConns`` Cumulative connections that failed to dial or were kicked
``bytesSent``    Cumulative bytes written (all packets + dribble bytes)
============== =============================================================

Reporter
~~~~~~~~

A dedicated goroutine wakes every second and prints a single-line status::

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
    go build -o gaslighter .

----

Usage
-----

.. code-block:: text

    gaslighter <ip[:port] | hostname> [flags]

    Flags:
      -s, --bloat-size int             handshake server-address string length (max 255) (default 255)
          --debug                      single-connection debug mode with colored packet log
      -d, --dribble-interval duration  interval between keep-alive bytes, online-mode fallback (default 5s)
          --har                         hit-and-run: don't wait for server response in pre-login mode
          -j, --join-delay duration        minimum gap between new connections (e.g. 4001ms)
              --prelogin                   pre-login spam mode: fire-and-forget login events
          -p, --proxies string             path to a file containing proxies (ip:port, one per line)
              --proxy-strategy string      proxy selection strategy: random or round-robin (default "random")
              --stall                      slow down the login sequence to glacial speeds
              --stall-duration duration    base wait between login steps (default 25s)
          -a, --access-token string        Mojang access token for online-mode auth

      -u, --player-uuid string         Mojang player UUID matching the access token
      -v, --verbose                    print per-connection TCP errors
      -w, --workers int                concurrent connections to maintain (default 10000)

Examples
~~~~~~~~

Default run against an offline-mode server::

    ./gaslighter 127.0.0.1:25565

SRV resolution (resolves _minecraft._tcp.play.server.com)::

    ./gaslighter play.server.com

Push harder — 50 000 workers, verbose errors::

    ./gaslighter 192.168.1.10:25565 -w 50000 -v

Respect a server throttle of one new login per 4 001 ms::

    ./gaslighter 96.9.213.246:25565 -j 4001ms

Online-mode server with Mojang credentials::

    ./gaslighter 192.168.1.10:25565 -a <access-token> -u <player-uuid>

Attack through a list of proxies::

    ./gaslighter 127.0.0.1:25565 -p proxies.txt

Inspect a single connection before running the full attack::

    ./gaslighter --debug 127.0.0.1:25565

Pre-login spam attack (high frequency)::

    ./gaslighter 127.0.0.1:25565 --prelogin

Maximum throughput pre-login spam (Hit-and-Run)::

    ./gaslighter 127.0.0.1:25565 --prelogin --har

----

Configuration File
------------------

The tool automatically searches for a configuration file named ``gaslighterc.toml`` in:

1. ``./gaslighterc.toml`` (current directory)
2. ``~/gaslighterc.toml`` (home directory)

If both exist, settings in the local file override those in the home directory.
CLI flags always take precedence over configuration file settings.

Example ``gaslighterc.toml``:

.. code-block:: toml

    proxies = "proxies.txt"
    proxy-strategy = "round-robin"
    workers = 5000
    join-delay = "1s"

----

Debug Mode
----------

``--debug`` opens exactly one connection and logs every packet in colour::

    19:43:09.357 → SEND  0x00  Handshake                          265 B
                          proto=767  host=nFAk40y...(255)  port=25565  next=Login
    19:43:09.357 → SEND  0x00  Login Start  name=IAl4BlmyN4fjzjNc  35 B
    19:43:09.357 [Handshake → Login]
    19:43:09.741 ← RECV  0x02  Login Success                       23 B  [db 86 01 ...]
    19:43:09.741 → SEND  0x03  Login Acknowledged                   2 B
    19:43:09.741 [Login → Configuration]
    19:43:09.742 ← RECV  0x07  Registry Data                    38712 B  [0a 0a 00 ...]
    19:43:09.742 ← RECV  0x03  Finish Configuration                  0 B
    19:43:09.742 → SEND  0x02  Acknowledge Configuration             2 B
    19:43:09.742 [Configuration → Play]
    19:43:09.742 ✓  Play state reached — holding indefinitely (Ctrl-C to stop)
    19:43:09.743 ← RECV  0x26  Keep Alive                           8 B  [00 00 00 00 ...]
    19:43:09.743 → SEND  0x18  Keep Alive Response  #1              10 B

Colour key:

* **Green** ``→ SEND`` — outbound packet
* **Cyan** ``← RECV`` — inbound packet
* **Yellow** ``[State → State]`` — protocol state transition
* **Bold green ✓** — success milestone
* **Bold red ✗** — error or server disconnect

For online-mode servers, the encryption handshake is also logged in full:
RSA key size, shared secret, server hash, and Mojang auth result.
The normal status reporter is suppressed in debug mode.

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
               ``-w`` or reduce ``-j`` to accelerate.
New/s drops    The target's Netty accept queue is saturating or the attacker
               OS is hitting a port/FD limit; check ``-v`` output.
Dropped spikes  Server is kicking connections (firewall, rate-limit plugin, or
               a GC pause causing Netty timeouts). For online-mode servers
               without credentials this is expected — the dribble fallback
               reconnects immediately.
============  ================================================================

----

Protocol Reference
------------------

The tool implements the full Minecraft Java Edition login sequence for
protocol 767 (1.21.1):

.. list-table::
   :header-rows: 1
   :widths: 15 15 15 55

   * - State
     - Direction
     - Packet ID
     - Purpose
   * - Handshake
     - C→S
     - ``0x00``
     - Declares protocol 767, oversized server address, next=Login
   * - Login
     - C→S
     - ``0x00``
     - Login Start — random 16-char username + zero UUID
   * - Login
     - S→C
     - ``0x03``
     - Set Compression (optional) — tracked, uncompressed payloads assumed
   * - Login
     - S→C
     - ``0x01``
     - Encryption Request — triggers RSA + AES/CFB8 handshake
   * - Login
     - C→S
     - ``0x01``
     - Encryption Response — RSA-encrypted shared secret + verify token
   * - Login
     - S→C
     - ``0x02``
     - Login Success — offline mode or post-authentication
   * - Login
     - C→S
     - ``0x03``
     - Login Acknowledged
   * - Configuration
     - S→C
     - ``0x03``
     - Finish Configuration
   * - Configuration
     - C→S
     - ``0x02``
     - Acknowledge Configuration
   * - Play
     - S→C
     - ``0x26``
     - Keep Alive — echoed back to hold the connection
   * - Play
     - C→S
     - ``0x18``
     - Keep Alive response

----

License
-------

For authorised testing of infrastructure you own or have explicit written
permission to test. Do not use against servers you do not control.
