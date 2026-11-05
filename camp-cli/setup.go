package campcli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/denisqq/agent-camp/camp-cli/clients"
	"github.com/denisqq/agent-camp/installer/localservice"
	"github.com/denisqq/agent-camp/installer/profiles"
	"github.com/denisqq/agent-camp/installer/setup"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/credstore"
	"github.com/spf13/cobra"
)

// setupCmd is the one-command onboarding path. Default mode prepares a
// local instance, starts camp-server, and integrates the selected agent
// clients. Flags make it fully noninteractive for agent-driven installs;
// `setup storage` and `setup agents` revisit one section.
func setupCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "setup",
		Short: "Prepare Camp and connect agent clients",
		Long: "One-command onboarding: configuration, local identity, starter\n" +
			"workspace, a running local camp-server, and client integrations.\n" +
			"Rerunning reuses identities and choices; a failure resumes safely.",
		RunE: func(cmd *cobra.Command, args []string) error {
			serverAddr, _ := cmd.Flags().GetString("server")
			name, _ := cmd.Flags().GetString("name")
			clients, _ := cmd.Flags().GetString("clients")
			jsonOut, _ := cmd.Flags().GetBool("json")
			startService, _ := cmd.Flags().GetBool("service")

			clientList := splitClients(clients)
			for _, c := range clientList {
				if !knownClient(c) {
					return fmt.Errorf("unknown client %q (supported: claude-code, claude-desktop, codex, cursor)", c)
				}
			}
			return setupRun(cmd.Context(), serverAddr, name, clientList, jsonOut, startService)
		},
	}
	c.Flags().String("server", "", "connect to an existing Camp server instead of a local instance")
	c.Flags().String("name", "", "agent display name (default: hostname)")
	c.Flags().String("clients", "", "comma-separated clients to integrate (claude-code,codex,cursor)")
	c.Flags().Bool("json", false, "machine-readable result")
	c.Flags().Bool("service", true, "start the local camp-server after setup")
	c.AddCommand(setupStorageCmd(), setupAgentsCmd())
	return c
}

func setupRun(ctx context.Context, serverAddr, name string, clientList []string, jsonOut, startService bool) error {
	// Local mode order: configuration, instance bootstrap, running
	// camp-server — only then identity registration and clients, which
	// need a reachable server.
	if serverAddr == "" {
		if err := ensureLocalConfig(); err != nil {
			return err
		}
		if err := bootstrapLocalInstance(); err != nil {
			return fmt.Errorf("local instance bootstrap: %w", err)
		}
		if startService {
			if err := startLocalService(); err != nil {
				fmt.Fprintf(os.Stderr, "camp: local service not started: %v (start it with camp up)\n", err)
			}
		}
		if cfg, err := loadConfig(config.Overrides{}); err == nil {
			serverAddr = "http://" + cfg.Listen
		} else {
			serverAddr = "http://127.0.0.1:8090"
		}
	}
	installer := &cliClientInstaller{}
	result, err := setup.Run(ctx, setup.Options{
		Server:          serverAddr,
		AgentName:       name,
		Clients:         clientList,
		Registerer:      httpRegisterer{},
		ClientInstaller: installer,
	})
	if err == nil && serverAddr != "" {
		result.Server = serverAddr
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	fmt.Printf("Camp setup complete.\nServer: %s\nWorkspace: %s\nAgent: %s (user %s)\n", result.Server, result.Workspace, result.AgentID, result.UserID)
	for _, c := range result.Clients {
		fmt.Printf("Client %s: configured — reload/reconnect the client to load its tools\n", c)
	}
	for _, c := range result.Pending {
		fmt.Printf("Client %s: incomplete — rerun camp setup\n", c)
	}
	fmt.Println("Check this installation later: camp doctor")
	return err
}

// ensureLocalConfig writes the default configuration when none exists,
// mirroring setup.Run's first stage so bootstrap and serve see the same
// file.
func ensureLocalConfig() error {
	path := config.DiscoverPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	cfg := config.Defaults(filepath.Join(filepath.Dir(path), "camp-data"))
	cfg.Registration.StarterWorkspace = "workspace"
	return config.Save(path, cfg)
}

// startLocalService launches camp-server under the instance lock.
func startLocalService() error {
	cfg, err := loadConfig(config.Overrides{})
	if err != nil {
		return err
	}
	binary, err := serverBinary()
	if err != nil {
		return err
	}
	service := localservice.Service{Dir: cfg.DataRoot, Command: binary, ConfigPath: config.DiscoverPath()}
	pid, err := service.Start(cfg.Listen)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "camp: camp-server running (pid %d) on %s\n", pid, cfg.Listen)
	return nil
}

// bootstrapLocalInstance initializes the local instance by delegating to
// camp-server setup (child process): the client binary never links the
// server engine. camp-server setup writes the configuration, bootstraps
// admin + starter workspace, and stores the admin credential privately.
func bootstrapLocalInstance() error {
	binary, err := serverBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, "setup")
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func makeToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

func knownClient(key string) bool {
	switch key {
	case "claude-code", "claude-desktop", "codex", "cursor":
		return true
	}
	return false
}

// cliClientInstaller bridges setup to clientconfig.
type cliClientInstaller struct{}

func (cliClientInstaller) Install(client, profileName string) error {
	p, err := profiles.Store{Dir: profiles.DefaultDir()}.Load(profileName)
	if err != nil {
		return err
	}
	result, err := clients.Install(clients.Client{Key: client}, clients.Scope{}, p, "")
	if err == nil {
		for _, f := range result.Files {
			fmt.Fprintf(os.Stderr, "camp: updated %s\n", f)
		}
	}
	return err
}

// setupStorageCmd rewrites the storage section: local directory (default)
// or S3. Validation failure preserves the previous working configuration.
func setupStorageCmd() *cobra.Command {
	var mode, endpoint, bucket, region, credentialsRef, accessKey, secretKey string
	c := &cobra.Command{
		Use:   "storage",
		Short: "Configure workspace storage (local directory or S3)",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.DiscoverPath()
			cfg, err := config.Load(path, config.Overrides{})
			if err != nil {
				return err
			}
			cfg.Storage.Mode = mode
			cfg.Storage.Endpoint, cfg.Storage.Bucket, cfg.Storage.Region = endpoint, bucket, region
			cfg.Storage.CredentialsRef = credentialsRef
			cfg.Storage.AccessKey, cfg.Storage.SecretKey = accessKey, secretKey
			if err := config.Save(path, cfg); err != nil {
				return fmt.Errorf("keeping previous configuration: %w", err)
			}
			if mode == "s3" && accessKey != "" && credentialsRef != "" {
				if err := (credstore.Store{Dir: credstore.DefaultDir()}).Save(credentialsRef, accessKey+":"+secretKey); err != nil {
					return err
				}
			}
			fmt.Println("Storage configuration saved (atomic; secrets stored separately).")
			return nil
		},
	}
	c.Flags().StringVar(&mode, "mode", "file", "storage mode: file or s3")
	c.Flags().StringVar(&endpoint, "endpoint", "", "S3 endpoint URL")
	c.Flags().StringVar(&bucket, "bucket", "", "S3 bucket")
	c.Flags().StringVar(&region, "region", "us-east-1", "S3 region")
	c.Flags().StringVar(&credentialsRef, "credentials-ref", "", "reference to a credential in private storage")
	c.Flags().StringVar(&accessKey, "access-key", "", "S3 access key (stored privately, never in the config)")
	c.Flags().StringVar(&secretKey, "secret-key", "", "S3 secret key (stored privately, never in the config)")
	return c
}

// setupAgentsCmd revisits client integration only.
func setupAgentsCmd() *cobra.Command {
	var clients string
	c := &cobra.Command{
		Use:   "agents",
		Short: "Revisit agent client integrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clients == "" {
				return fmt.Errorf("--clients is required")
			}
			clientList := splitClients(clients)
			for _, c := range clientList {
				if !knownClient(c) {
					return fmt.Errorf("unknown client %q", c)
				}
			}
			return setupRun(cmd.Context(), "", "", clientList, false, false)
		},
	}
	c.Flags().StringVar(&clients, "clients", "", "comma-separated clients (claude-code,codex,cursor)")
	return c
}

func splitClients(clients string) []string {
	var out []string
	for _, c := range strings.Split(clients, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// healthPing reports whether a camp server answers on addr.
func healthPing(addr string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// statusCommand reports the local service state.
func statusCommand(args []string) error {
	cfg, err := loadConfig(config.Overrides{})
	if err != nil {
		return err
	}
	service := localservice.Service{Dir: cfg.DataRoot, Command: "", ConfigPath: config.DiscoverPath()}
	pid, _ := service.Running()
	running := pid != 0 && healthPing(cfg.Listen)
	fmt.Printf("profile: %s\nserver: %s\nstate: %s\n", config.DiscoverPath(), cfg.Listen, map[bool]string{true: "running", false: "stopped"}[running])
	if running {
		fmt.Printf("pid: %d\n", pid)
	}
	return nil
}

func upCommand(args []string) error {
	cfg, err := loadConfig(config.Overrides{})
	if err != nil {
		return err
	}
	binary, err := serverBinary()
	if err != nil {
		return err
	}
	service := localservice.Service{Dir: cfg.DataRoot, Command: binary, ConfigPath: config.DiscoverPath()}
	pid, err := service.Start(cfg.Listen)
	if err != nil {
		return err
	}
	fmt.Printf("camp-server running (pid %d) on %s\n", pid, cfg.Listen)
	return nil
}

func downCommand(args []string) error {
	cfg, err := loadConfig(config.Overrides{})
	if err != nil {
		return err
	}
	service := localservice.Service{Dir: cfg.DataRoot, Command: "", ConfigPath: config.DiscoverPath()}
	if err := service.Stop(); err != nil {
		return err
	}
	fmt.Println("camp-server stopped; data preserved")
	return nil
}
