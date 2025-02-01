package monitor

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

// DiscoverBroker scans local network for an MQTT broker on port 1883
// Returns the IP address of the first broker found
func DiscoverBroker() (string, error) {
	// Get local IP address and netmask
	ip, mask, err := getLocalIPAndMask()
	if err != nil {
		return "", fmt.Errorf("failed to get local IP: %v", err)
	}

	// Check if we're on a /24 network
	ones, bits := mask.Size()
	if bits-ones != 8 {
		return "", fmt.Errorf("not scanning, network mask must be /24 (255.255.255.0), got /%d", ones)
	}

	// Extract subnet (192.168.x)
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("invalid IP format: %s", ip)
	}
	subnet := fmt.Sprintf("%s.%s.%s", parts[0], parts[1], parts[2])

	// Scan addresses in subnet
	for i := 1; i < 255; i++ {
		target := fmt.Sprintf("%s.%d:1883", subnet, i)
		logging.InfoLogger.Printf("Scanning %s", target)
		if IsPortOpen(target) {
			return target, nil
		}
	}

	logging.InfoLogger.Printf("no owlcms found in subnet %s", subnet)
	return "", fmt.Errorf("owlcms not found")
}

// getLocalIPAndMask returns the non-loopback local IP and netmask of the host
func getLocalIPAndMask() (string, net.IPMask, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", nil, err
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					ip := ip4.String()
					if strings.HasPrefix(ip, "192.168") {
						return ip, ipnet.Mask, nil
					}
				}
			}
		}
	}
	return "", nil, fmt.Errorf("no suitable local IP address found")
}

// IsPortOpen tests if a port is open by attempting to connect
func IsPortOpen(address string) bool {
	timeout := 100 * time.Millisecond
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}

func UpdateOwlcmsAddress(cfg *config.Config, configFile string) (string, error) {
	broker := cfg.OwlCMS
	owlcmsAddress := fmt.Sprintf("%s:1883", broker)
	if cfg.OwlCMS != "" && IsPortOpen(owlcmsAddress) {
		logging.InfoLogger.Printf("OwlCMS broker is reachable at %s\n", owlcmsAddress)
	} else {
		logging.InfoLogger.Printf("OwlCMS broker is not reachable at %s, scanning for brokers...\n", owlcmsAddress)
		var err error
		broker, err = DiscoverBroker()
		if err != nil {
			fmt.Printf("Error discovering broker: %v\n", err)
			return broker, err
		}
		logging.InfoLogger.Printf("Broker found: %s\n", broker)
		// remove the port number
		broker = strings.Split(broker, ":")[0]
		cfg.OwlCMS = broker
		if err := config.UpdateConfigFile(configFile, broker); err != nil {
			fmt.Printf("Error updating config file: %v\n", err)
		}
	}
	return broker, nil
}
