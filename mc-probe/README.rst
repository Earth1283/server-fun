mc-probe
========

The "Intelligence Officer" of the ``server-fun`` arsenal. While others go in
swinging, ``mc-probe`` is the quiet observer in the corner taking notes on your
target's weaknesses. It performs standard Server List Pings (SLP) and deep
protocol probes to map out exactly what you're dealing with.

Features for the Sophisticated Spy
----------------------------------

* **SLP Surveillance** — Extracts MOTD, player counts, and versions without
  ever leaving a "Join" log.
* **Deep Probe** — Determines online/offline status, compression thresholds,
  and RSA key sizes. If the server is naked (offline mode), we'll know.
* **Proxy Stealth** — Supports the same SOCKS5 and SRV magic as Gaslighter,
  because reconnaissance should be invisible.

Usage
-----

.. code-block:: text

    mc-probe-bin <ip[:port] | hostname> [flags]

    Flags:
      -p, --proxies string          path to .txt file with SOCKS5 proxies
          --proxy-strategy string   proxy strategy: random or round-robin (default "random")

Examples
~~~~~~~~

Standard recon run::

    ./mc-probe-bin mc.hypixel.net

Quiet recon via proxies::

    ./mc-probe-bin play.target.com -p proxies.txt --proxy-strategy round-robin

Why Use It?
-----------

Because knowledge is power. Before you commit thousands of workers to a
heap-exhaustion attack, it's polite to check if the server is running on a
Toaster or a supercomputer. ``mc-probe`` provides the "empirical validation"
required to choose the right tool for the job.

*Happy sniffing!* 🕵️
