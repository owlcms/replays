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
	_, ipNet, err := getLocalIPAndNetmask()
	if err != nil {
		return "", err
	}

	// Calculate the number of hosts in the subnet
	ones, bits := ipNet.Mask.Size()
	numHosts := 1 << (bits - ones)

	// Perform a scan if there are fewer than 255 machines in the subnet
	if numHosts <= 256 {
		return scanNetworkForBroker(ipNet)
	}

	return "", fmt.Errorf("network too large to scan")
}

// getLocalIPAndNetmask returns the non-loopback local IP and netmask of the host
func getLocalIPAndNetmask() (net.IP, *net.IPNet, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, nil, err
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP, ipNet, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("no IP address found")
}

// scanNetworkForBroker scans the network to find the MQTT broker address
func scanNetworkForBroker(ipNet *net.IPNet) (string, error) {
	ip := ipNet.IP.Mask(ipNet.Mask)
	for ip := ip.Mask(ipNet.Mask); ipNet.Contains(ip); inc(ip) {
		address := fmt.Sprintf("%s:1883", ip.String())
		logging.InfoLogger.Printf("Trying address: %s", address)
		if IsPortOpen(address) {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("MQTT broker not found on the network")
}

// inc increments an IP address
func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// IsPortOpen tests if a port is open by attempting to connect
func IsPortOpen(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
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
			return broker, err
		}
	}
	return broker, nil
}
