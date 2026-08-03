package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	proxyPath       string
	strategy        string
	jsonOutput      bool
	outputFile      string
	noColor         bool
	protocolVersion int
	inputFile       string
)

type ProbeReport struct {
	Target string       `json:"target"`
	Host   string       `json:"host"`
	Port   uint16       `json:"port"`
	SLP    *SLPResponse `json:"slp,omitempty"`
	Probe  *ProbeResult `json:"probe,omitempty"`
	Error  string       `json:"error,omitempty"`
}

var rootCmd = &cobra.Command{
	Use:   "mc-probe [ip[:port] | hostname]",
	Short: "Minecraft Server Intelligence Gathering Probe",
	Long:  "Performs standard Server List Pings (SLP) and deep protocol probes on target Minecraft servers to map out version, player counts, proxy protection, and auth mode.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if noColor {
			disableColors()
		}

		// Load Proxies if specified
		pPath := cmd.Flag("proxies").Value.String()
		if pPath == "" {
			pPath = viper.GetString("proxies")
		}
		if pPath != "" {
			if err := loadProxyList(pPath); err != nil {
				return fmt.Errorf("failed to load proxies: %w", err)
			}
			if !jsonOutput {
				fmt.Printf("%s[Proxy]%s Loaded %d proxies from %s\n", cBoldGreen, cReset, len(proxyPool), pPath)
			}
		}

		var targets []string
		if inputFile != "" {
			lines, err := readLines(inputFile)
			if err != nil {
				return fmt.Errorf("failed to read input file: %w", err)
			}
			targets = lines
		} else if len(args) == 1 {
			targets = []string{args[0]}
		} else {
			return fmt.Errorf("must specify either a target host or --input-file")
		}

		var reports []ProbeReport
		for _, rawTarget := range targets {
			rawTarget = strings.TrimSpace(rawTarget)
			if rawTarget == "" || strings.HasPrefix(rawTarget, "#") {
				continue
			}
			report := probeTarget(rawTarget)
			reports = append(reports, report)
			if !jsonOutput && len(targets) > 1 {
				fmt.Println(strings.Repeat("─", 60))
			}
		}

		if jsonOutput {
			var data []byte
			var err error
			if len(reports) == 1 {
				data, err = json.MarshalIndent(reports[0], "", "  ")
			} else {
				data, err = json.MarshalIndent(reports, "", "  ")
			}
			if err != nil {
				return fmt.Errorf("json marshal error: %w", err)
			}
			if outputFile != "" {
				if err := os.WriteFile(outputFile, data, 0644); err != nil {
					return fmt.Errorf("failed writing output file: %w", err)
				}
				fmt.Printf("Report saved to %s\n", outputFile)
			} else {
				fmt.Println(string(data))
			}
			return nil
		}

		if outputFile != "" {
			// Write formatted text report to file
			var sb strings.Builder
			for _, r := range reports {
				sb.WriteString(formatTextReport(r, true))
			}
			if err := os.WriteFile(outputFile, []byte(sb.String()), 0644); err != nil {
				return fmt.Errorf("failed writing output file: %w", err)
			}
			fmt.Printf("Report saved to %s\n", outputFile)
		}

		return nil
	},
}

func probeTarget(inputTarget string) ProbeReport {
	host, port, srvInfo, err := resolveTarget(inputTarget)
	report := ProbeReport{
		Target: inputTarget,
		Host:   host,
		Port:   port,
	}

	if err != nil {
		report.Error = err.Error()
		if !jsonOutput {
			fmt.Printf("%s[Error]%s Could not resolve target %s: %v\n", cBoldRed, cReset, inputTarget, err)
		}
		return report
	}

	targetAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	if !jsonOutput {
		if srvInfo != "" {
			fmt.Printf("%s[SRV]%s %s\n", cBoldCyan, cReset, srvInfo)
		}
		fmt.Printf("\n%sProbing %s...%s\n\n", cBoldYellow, targetAddr, cReset)
	}

	// Phase 1: SLP
	slp, slpErr := doSLP(host, port, protocolVersion)
	if slpErr != nil {
		if !jsonOutput {
			fmt.Printf("%s[SLP] Failed: %v%s\n", cBoldRed, slpErr, cReset)
		}
	} else {
		report.SLP = slp
	}

	// Phase 2: Protocol Probe
	probe, probeErr := doProbe(host, port, protocolVersion)
	if probeErr != nil {
		if !jsonOutput {
			fmt.Printf("%s[Probe] Failed: %v%s\n", cBoldRed, probeErr, cReset)
		}
	} else {
		report.Probe = probe
	}

	if !jsonOutput {
		fmt.Print(formatTextReport(report, false))
	}

	return report
}

func resolveTarget(input string) (string, uint16, string, error) {
	host, portStr, err := net.SplitHostPort(input)
	if err != nil {
		// No explicit port provided
		if net.ParseIP(input) != nil {
			return input, 25565, "", nil
		}
		// Try SRV resolution
		_, addrs, srvErr := net.LookupSRV("minecraft", "tcp", input)
		if srvErr == nil && len(addrs) > 0 {
			targetHost := strings.TrimSuffix(addrs[0].Target, ".")
			targetPort := addrs[0].Port
			srvInfo := fmt.Sprintf("Resolved SRV %s -> %s:%d", input, targetHost, targetPort)
			return targetHost, targetPort, srvInfo, nil
		}
		return input, 25565, "", nil
	}

	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", 0, "", fmt.Errorf("invalid port %s", portStr)
	}
	return host, uint16(p), "", nil
}

func formatTextReport(r ProbeReport, plain bool) string {
	var sb strings.Builder
	reset := cReset
	cyan := cCyan
	boldCyan := cBoldCyan
	boldYellow := cBoldYellow
	boldRed := cBoldRed

	if plain || noColor {
		reset = ""
		cyan = ""
		boldCyan = ""
		boldYellow = ""
		boldRed = ""
	}

	if r.SLP != nil {
		slp := r.SLP
		sb.WriteString(fmt.Sprintf("%s─── Server List Ping ───────────────────────────────────────%s\n", boldCyan, reset))
		sb.WriteString(fmt.Sprintf("%sVersion:%s    %s (Protocol %d)\n", cyan, reset, slp.Version.Name, slp.Version.Protocol))
		sb.WriteString(fmt.Sprintf("%sPlayers:%s    %d/%d\n", cyan, reset, slp.Players.Online, slp.Players.Max))
		if len(slp.Players.Sample) > 0 {
			var names []string
			for _, p := range slp.Players.Sample {
				if p.Name != "" {
					names = append(names, p.Name)
				}
			}
			if len(names) > 0 {
				sb.WriteString(fmt.Sprintf("%sSample:%s     %s\n", cyan, reset, strings.Join(names, ", ")))
			}
		}
		sb.WriteString(fmt.Sprintf("%sMOTD:%s       %s\n", cyan, reset, strings.ReplaceAll(slp.MOTD(), "\n", " ")))
		if slp.RTT > 0 {
			sb.WriteString(fmt.Sprintf("%sLatency:%s    %v\n", cyan, reset, slp.RTT.Round(time.Millisecond)))
		}
		if slp.Favicon != "" {
			sb.WriteString(fmt.Sprintf("%sFavicon:%s    Present\n", cyan, reset))
		}
		sb.WriteString("\n")
	}

	if r.Probe != nil {
		res := r.Probe
		sb.WriteString(fmt.Sprintf("%s─── Protocol Deep Probe ────────────────────────────────────%s\n", boldYellow, reset))
		if res.ServerFingerprint != "" {
			sb.WriteString(fmt.Sprintf("%sFingerprint:%s %s\n", cyan, reset, res.ServerFingerprint))
		}
		mode := "Offline"
		if res.OnlineMode {
			mode = "Online (Authenticated)"
		}
		sb.WriteString(fmt.Sprintf("%sAuth Mode:%s  %s\n", cyan, reset, mode))
		if res.OnlineMode && res.RSAKeySize > 0 {
			sb.WriteString(fmt.Sprintf("%sRSA Key:%s    %d bits\n", cyan, reset, res.RSAKeySize))
		}
		if res.ProxyEnforced {
			sb.WriteString(fmt.Sprintf("%sProxy Auth:%s Enforced (%s)%s\n", boldRed, reset, res.ProxyChannel, reset))
		}
		if res.DisconnectReason != "" {
			sb.WriteString(fmt.Sprintf("%sDisconnect:%s %s\n", cyan, reset, res.DisconnectReason))
		}
		if res.Compression >= 0 {
			sb.WriteString(fmt.Sprintf("%sCompression:%s Threshold %d\n", cyan, reset, res.Compression))
		} else {
			sb.WriteString(fmt.Sprintf("%sCompression:%s Disabled\n", cyan, reset))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func disableColors() {
	cReset = ""
	cDim = ""
	cBoldGreen = ""
	cBoldCyan = ""
	cBoldRed = ""
	cBoldYellow = ""
	cGreen = ""
	cCyan = ""
	cGray = ""
}

func loadProxyList(path string) error {
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	proxyPool = append(proxyPool, lines...)
	return nil
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func init() {
	home, _ := os.UserHomeDir()
	viper.AddConfigPath(home)
	viper.AddConfigPath(".")
	viper.SetConfigName("gaslighterc")
	viper.SetConfigType("toml")
	viper.AutomaticEnv()
	viper.ReadInConfig()

	f := rootCmd.Flags()
	f.StringVarP(&proxyPath, "proxies", "p", "", "path to .txt file with SOCKS5 proxies")
	f.StringVar(&strategy, "proxy-strategy", "random", "proxy strategy: random or round-robin")
	f.BoolVarP(&jsonOutput, "json", "j", false, "output probe results as JSON")
	f.StringVarP(&outputFile, "output", "o", "", "path to output file")
	f.BoolVar(&noColor, "no-color", false, "disable ANSI color output")
	f.IntVar(&protocolVersion, "protocol", 767, "Minecraft protocol version (default 767 for MC 1.21)")
	f.StringVarP(&inputFile, "input-file", "i", "", "path to text file containing list of target IPs/hostnames")
	viper.BindPFlags(f)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

