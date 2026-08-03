wiretap
========

The "Intelligence Officer" of the ``server-fun`` arsenal. While others go in
swinging, ``wiretap`` is the quiet observer in the corner taking notes on your
target's weaknesses. It performs standard Server List Pings (SLP) and deep
protocol probes to map out exactly what you're dealing with.

Features for the Sophisticated Spy
----------------------------------

* **SLP Surveillance** — Extracts MOTD, player counts, player samples, and versions without
  ever leaving a "Join" log.
* **Ping RTT Latency** — Measures round-trip ping time to target server in milliseconds.
* **Deep Probe & Fingerprinting** — Determines online/offline status, compression thresholds,
  RSA key sizes, proxy enforcement (Velocity / BungeeGuard), disconnect reasons, and server stack fingerprints.
* **Structured Output & Batch Scan** — Export intelligence as JSON or plain text files, disable colors for piping, and scan multiple targets from an input file.
* **Proxy Stealth** — Supports SOCKS5 proxy rotation and DNS SRV target resolution.

Usage
-----

.. code-block:: text

    wiretap-bin [ip[:port] | hostname] [flags]

    Flags:
      -p, --proxies string          path to .txt file with SOCKS5 proxies
          --proxy-strategy string   proxy strategy: random or round-robin (default "random")
      -j, --json                    output probe results as JSON
      -o, --output string           path to output file
          --no-color                disable ANSI color output
          --protocol int            Minecraft protocol version (default 767)
      -i, --input-file string       path to text file containing list of target IPs/hostnames

Examples
~~~~~~~~

Standard recon run::

    ./wiretap-bin mc.hypixel.net

Export JSON intelligence report::

    ./wiretap-bin mc.hypixel.net -j -o report.json

Batch scan multiple targets quiet recon via proxies::

    ./wiretap-bin -i targets.txt -p proxies.txt --proxy-strategy round-robin -j -o batch_results.json

Why Use It?
-----------

Because knowledge is power. Before you commit thousands of workers to a
heap-exhaustion attack, it's polite to check if the server is running on a
Toaster or a supercomputer. ``wiretap`` provides the "empirical validation"
required to choose the right tool for the job.

*Happy sniffing!* 🕵️

