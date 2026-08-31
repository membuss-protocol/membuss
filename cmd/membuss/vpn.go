package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newVPNCmd() *cobra.Command {
	vpnCmd := &cobra.Command{
		Use:   "vpn",
		Short: "Manage WireGuard client profiles, P2P mesh network, and exit nodes",
	}

	// 1. Status
	vpnCmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show WireGuard server status, Virtual IP, and mesh telemetry",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := vpnAPIGet("/api/v1/vpn/status")
			if err != nil {
				return err
			}

			var status struct {
				Enabled          bool   `json:"enabled"`
				MeshID           string `json:"mesh_id"`
				VirtualIP        string `json:"virtual_ip"`
				WGServerPort     int    `json:"wg_server_port"`
				WGServerPubKey   string `json:"wg_server_public_key"`
				WGServerEndpoint string `json:"wg_server_endpoint"`
				WGDevicesCount   int    `json:"wg_devices_count"`
				IsExitNode       bool   `json:"is_exit_node"`
				SelectedExitNode string `json:"selected_exit_node"`
				PeerCount        int    `json:"peer_count"`
			}
			if err := json.Unmarshal(resp, &status); err != nil {
				return fmt.Errorf("parse status: %w", err)
			}

			fmt.Println("⚡ MemVPN WireGuard & Mesh Status:")
			fmt.Printf("  Status:            Active (Enabled)\n")
			fmt.Printf("  Mesh ID:           %s\n", status.MeshID)
			fmt.Printf("  Virtual IP:        %s\n", status.VirtualIP)
			fmt.Printf("  WireGuard Server:  %s (Port %d)\n", status.WGServerEndpoint, status.WGServerPort)
			fmt.Printf("  Server Public Key: %s\n", status.WGServerPubKey)
			fmt.Printf("  Client Devices:    %d registered\n", status.WGDevicesCount)
			fmt.Printf("  Connected Peers:   %d\n", status.PeerCount)
			fmt.Printf("  Exit Provider:     %v\n", status.IsExitNode)
			if status.SelectedExitNode != "" {
				fmt.Printf("  Internet Routing:  Active (Exit: %s)\n", status.SelectedExitNode)
			} else {
				fmt.Printf("  Internet Routing:  Local Egress\n")
			}
			return nil
		},
	})

	// 2. WireGuard Config / Export
	vpnCmd.AddCommand(&cobra.Command{
		Use:   "config [device_name]",
		Short: "Display or export WireGuard .conf configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			device := "default"
			if len(args) > 0 && args[0] != "" {
				device = args[0]
			}

			resp, err := vpnAPIGet(fmt.Sprintf("/api/v1/vpn/wg/profile?device=%s", device))
			if err != nil {
				return err
			}

			var profile struct {
				DeviceName     string `json:"device_name"`
				VirtualIP      string `json:"virtual_ip"`
				ServerEndpoint string `json:"server_endpoint"`
				ConfigText     string `json:"config_text"`
			}
			if err := json.Unmarshal(resp, &profile); err != nil {
				return fmt.Errorf("parse profile: %w", err)
			}

			fmt.Println(profile.ConfigText)
			return nil
		},
	})

	// 3. QR Code helper
	vpnCmd.AddCommand(&cobra.Command{
		Use:   "qr [device_name]",
		Short: "Display 1-Click WireGuard Mobile QR Code setup instructions",
		RunE: func(cmd *cobra.Command, args []string) error {
			device := "default"
			if len(args) > 0 && args[0] != "" {
				device = args[0]
			}

			resp, err := vpnAPIGet(fmt.Sprintf("/api/v1/vpn/wg/profile?device=%s", device))
			if err != nil {
				return err
			}

			var profile struct {
				DeviceName     string `json:"device_name"`
				VirtualIP      string `json:"virtual_ip"`
				ServerEndpoint string `json:"server_endpoint"`
				DownloadURL    string `json:"download_url"`
			}
			_ = json.Unmarshal(resp, &profile)

			fmt.Printf("\n📱 1-Click WireGuard Mobile & Desktop Setup (%s):\n", profile.DeviceName)
			fmt.Printf("  Virtual IP:      %s\n", profile.VirtualIP)
			fmt.Printf("  Server Endpoint: %s\n", profile.ServerEndpoint)
			fmt.Println("\nTo connect your phone or laptop:")
			fmt.Println("  1. Install the official WireGuard app (iOS App Store / Google Play / macOS / Windows).")
			fmt.Println("  2. Open the Web Explorer at http://localhost:8083/explorer/vpn to scan the instant QR Code.")
			fmt.Println("  3. Or download the .conf profile:")
			fmt.Printf("     http://localhost:8083%s\n\n", profile.DownloadURL)
			return nil
		},
	})

	// 4. Device Management
	devCmd := &cobra.Command{
		Use:   "device",
		Short: "Manage registered WireGuard client devices",
	}

	devCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all registered client devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := vpnAPIGet("/api/v1/vpn/wg/devices")
			if err != nil {
				return err
			}

			var devices []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				PublicKey string `json:"public_key"`
				VirtualIP string `json:"virtual_ip"`
				Connected bool   `json:"connected"`
				Endpoint  string `json:"endpoint"`
			}
			if err := json.Unmarshal(resp, &devices); err != nil {
				return fmt.Errorf("parse devices: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tVIRTUAL IP\tPUBLIC KEY\tCONNECTED\tENDPOINT")
			for _, d := range devices {
				pubShort := d.PublicKey
				if len(pubShort) > 12 {
					pubShort = pubShort[:12] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\n", d.Name, d.VirtualIP, pubShort, d.Connected, d.Endpoint)
			}
			return w.Flush()
		},
	})

	devCmd.AddCommand(&cobra.Command{
		Use:   "add <name>",
		Short: "Register a new WireGuard client device profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reqBody, _ := json.Marshal(map[string]string{"name": args[0]})
			resp, err := vpnAPIPost("/api/v1/vpn/wg/device", reqBody)
			if err != nil {
				return err
			}

			var profile struct {
				DeviceName  string `json:"device_name"`
				VirtualIP   string `json:"virtual_ip"`
				ConfigText  string `json:"config_text"`
				DownloadURL string `json:"download_url"`
			}
			if err := json.Unmarshal(resp, &profile); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}

			fmt.Printf("✓ Created WireGuard device profile %q!\n", profile.DeviceName)
			fmt.Printf("  Virtual IP:   %s\n", profile.VirtualIP)
			fmt.Printf("  Download URL: %s\n", profile.DownloadURL)
			return nil
		},
	})

	vpnCmd.AddCommand(devCmd)

	// 5. Exit Node Routing
	exitCmd := &cobra.Command{
		Use:   "exit",
		Short: "Manage decentralized internet exit nodes",
	}

	exitCmd.AddCommand(&cobra.Command{
		Use:   "select <peer_id|auto|none>",
		Short: "Select an exit node to route all internet traffic through",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			peerID := args[0]
			if peerID == "none" || peerID == "off" {
				peerID = ""
			}
			reqBody, _ := json.Marshal(map[string]string{"peer_id": peerID})
			_, err := vpnAPIPost("/api/v1/vpn/exit/select", reqBody)
			if err != nil {
				return err
			}
			if peerID == "" {
				fmt.Println("✓ Swarm internet routing disabled (using local egress)")
			} else {
				fmt.Printf("✓ All internet traffic routed through Exit Node: %s\n", peerID)
			}
			return nil
		},
	})

	vpnCmd.AddCommand(exitCmd)

	// 6. Expose Service
	vpnCmd.AddCommand(&cobra.Command{
		Use:   "expose <name> <target_addr>",
		Short: "Expose a local service or port to the P2P mesh network",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			reqBody, _ := json.Marshal(map[string]any{
				"name":        args[0],
				"target_addr": args[1],
			})
			_, err := vpnAPIPost("/api/v1/vpn/expose", reqBody)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Service %q exposed to mesh on target %s\n", args[0], args[1])
			return nil
		},
	})

	// 7. Forward Service
	vpnCmd.AddCommand(&cobra.Command{
		Use:   "forward <local_addr> <remote_peer_id> <remote_service>",
		Short: "Bind a local port to a remote peer's service over the mesh",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			reqBody, _ := json.Marshal(map[string]string{
				"local_addr":     args[0],
				"remote_peer_id": args[1],
				"remote_service": args[2],
			})
			_, err := vpnAPIPost("/api/v1/vpn/forward", reqBody)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Local port %s forwarded to service %s on peer %s\n", args[0], args[2], args[1])
			return nil
		},
	})

	return vpnCmd
}

func vpnAPIGet(path string) ([]byte, error) {
	baseURL := "http://127.0.0.1:5004"
	if custom := os.Getenv("MEMBUSS_API_ADDR"); custom != "" {
		if !strings.HasPrefix(custom, "http://") && !strings.HasPrefix(custom, "https://") {
			custom = "http://" + custom
		}
		baseURL = custom
	}

	req, err := http.NewRequest("GET", baseURL+path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon connection failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var env struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
		Err  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.OK {
		return env.Data, nil
	}
	return body, nil
}

func vpnAPIPost(path string, reqBody []byte) ([]byte, error) {
	baseURL := "http://127.0.0.1:5004"
	if custom := os.Getenv("MEMBUSS_API_ADDR"); custom != "" {
		if !strings.HasPrefix(custom, "http://") && !strings.HasPrefix(custom, "https://") {
			custom = "http://" + custom
		}
		baseURL = custom
	}

	req, err := http.NewRequest("POST", baseURL+path, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon connection failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(body))
	}

	var env struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
		Err  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.OK {
		return env.Data, nil
	}
	return body, nil
}
