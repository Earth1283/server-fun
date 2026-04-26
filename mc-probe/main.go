package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	proxyPath string
	strategy  string
)

var rootCmd = &cobra.Command{
	Use:   "mc-probe <ip[:port] | hostname>",
	Short: "Minecraft Server Intelligence Gathering Probe",
	Long:  "Performs a comprehensive audit on a target Minecraft server to extract SLP and protocol details.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputTarget := args[0]
		var target string

		// 1. Resolve Target (SRV Fallback)
		_, portStr, err := net.SplitHostPort(inputTarget)
		if err != nil {
			if net.ParseIP(inputTarget) != nil {
				target = net.JoinHostPort(inputTarget, "25565")
			} else {
				_, addrs, srvErr := net.LookupSRV("minecraft", "tcp", inputTarget)
				if srvErr == nil && len(addrs) > 0 {
					target = net.JoinHostPort(addrs[0].Target, strconv.Itoa(int(addrs[0].Port)))
					fmt.Printf("%s[SRV]%s Resolved %s -> %s\n", cBoldCyan, cReset, inputTarget, target)
				} else {
					target = net.JoinHostPort(inputTarget, "25565")
				}
			}
		} else {
			target = inputTarget
			// Verify port is valid int
			_, err = strconv.Atoi(portStr)
			if err != nil {
				return fmt.Errorf("invalid port in %s", inputTarget)
			}
		}

		// 2. Load Proxies
		if proxyPath != "" {
			if err := loadProxyList(proxyPath); err != nil {
				return fmt.Errorf("failed to load proxies: %w", err)
			}
			fmt.Printf("%s[Proxy]%s Loaded %d proxies from %s\n", cBoldGreen, cReset, len(proxyPool), proxyPath)
		}

		fmt.Printf("\n%sProbing %s...%s\n\n", cBoldYellow, target, cReset)

		// 3. Phase 1: Server List Ping
		slp, err := doSLP(target)
		if err != nil {
			fmt.Printf("%s[SLP] Failed: %v%s\n", cBoldRed, err, cReset)
		} else {
			fmt.Printf("%s─── Server List Ping ───────────────────────────────────────%s\n", cBoldCyan, cReset)
			fmt.Printf("%sVersion:%s    %s (Protocol %d)\n", cCyan, cReset, slp.Version.Name, slp.Version.Protocol)
			fmt.Printf("%sPlayers:%s    %d/%d\n", cCyan, cReset, slp.Players.Online, slp.Players.Max)
			fmt.Printf("%sMOTD:%s       %s\n", cCyan, cReset, strings.ReplaceAll(slp.MOTD(), "\n", " "))
			if slp.Favicon != "" {
				fmt.Printf("%sFavicon:%s    Present\n", cCyan, cReset)
			}
			fmt.Println()
		}

		// 4. Phase 2: Deep Protocol Probe
		res, err := doProbe(target)
		if err != nil {
			fmt.Printf("%s[Probe] Failed: %v%s\n", cBoldRed, err, cReset)
		} else {
			fmt.Printf("%s─── Protocol Deep Probe ────────────────────────────────────%s\n", cBoldYellow, cReset)
			mode := "Offline"
			if res.OnlineMode {
				mode = "Online (Authenticated)"
			}
			fmt.Printf("%sAuth Mode:%s  %s\n", cCyan, cReset, mode)
			if res.OnlineMode && res.RSAKeySize > 0 {
				fmt.Printf("%sRSA Key:%s    %d bits\n", cCyan, cReset, res.RSAKeySize)
			}
			if res.Compression >= 0 {
				fmt.Printf("%sCompression:%s Threshold %d\n", cCyan, cReset, res.Compression)
			} else {
				fmt.Printf("%sCompression:%s Disabled\n", cCyan, cReset)
			}
			fmt.Println()
		}

		return nil
	},
}

func loadProxyList(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			proxyPool = append(proxyPool, line)
		}
	}
	return nil
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
	viper.BindPFlags(f)

	if proxyPath == "" {
		proxyPath = viper.GetString("proxies")
	}
	if strategy == "random" {
		strategy = viper.GetString("proxy-strategy")
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
