package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/denisqq/agent-camp/internal/clientconfig"
	"github.com/denisqq/agent-camp/internal/config"
	"github.com/denisqq/agent-camp/internal/localservice"
	"github.com/denisqq/agent-camp/internal/server"
	"github.com/denisqq/agent-camp/internal/setup"
)

// camp setup is the one-command onboarding path. Default mode prepares a
// local instance (configuration, data, identity, starter workspace),
// starts the local service, and integrates the selected agent clients.
// Flags make it fully noninteractive for agent-driven installation;
// `camp setup storage`, `camp setup agents`, and `camp setup server`
// revisit one section.
func setupCommand(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "storage":
			return setupStorage(args[1:])
		case "agents":
			return setupAgents(args[1:])
		case "server":
			return setupServer(args[1:])
		}
	}
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	server := flags.String("server", "", "connect to an existing Camp server instead of a local instance")
	name := flags.String("name", "", "agent display name (default: hostname)")
	clients := flags.String("clients", "", "comma-separated clients to integrate (claude-code,codex,cursor)")
	jsonOut := flags.Bool("json", false, "machine-readable result")
	startService := flags.Bool("service", true, "start the local server after setup")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var clientList []string
	if *clients != "" {
		for _, c := range strings.Split(*clients, ",") {
			if c = strings.TrimSpace(c); c != "" {
				clientList = append(clientList, c)
			}
		}
		for _, c := range clientList {
			if !knownClient(c) {
				return fmt.Errorf("unknown client %q (supported: claude-code, claude-desktop, codex, cursor)", c)
			}
		}
	}

	installer := &cliClientInstaller{}
	result, err := setup.Run(context.Background(), setup.Options{
		Server:          *server,
		AgentName:       *name,
		Clients:         clientList,
		Registerer:      httpRegisterer{},
		ClientInstaller: installer,
	})
	if err == nil && *server == "" && *startService {
		cfg, cfgErr := loadConfig(config.Overrides{})
		if cfgErr == nil {
			launcher, _ := absoluteLauncher()
			service := localservice.Service{Dir: cfg.DataRoot, Command: launcher, ConfigPath: configPath()}
			if pid, sErr := service.Start(cfg.Listen); sErr == nil {
				result.Server = "http://" + cfg.Listen
				fmt.Fprintf(os.Stderr, "camp: local server running (pid %d) on %s\n", pid, cfg.Listen)
			} else {
				fmt.Fprintf(os.Stderr, "camp: local service not started: %v (start it with camp up)\n", sErr)
			}
		}
	}
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(result)
	} else {
		fmt.Printf("Camp setup complete.\nServer: %s\nWorkspace: %s\nAgent: %s (user %s)\n", result.Server, result.Workspace, result.AgentID, result.UserID)
		for _, c := range result.Clients {
			fmt.Printf("Client %s: configured — reload/reconnect the client to load its tools\n", c)
		}
		for _, c := range result.Pending {
			fmt.Printf("Client %s: incomplete — rerun camp setup\n", c)
		}
		fmt.Println("Check this installation later: camp doctor")
	}
	return err
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
	p, err := clientconfig.Store{Dir: clientconfig.DefaultStoreDir()}.LoadProfile(profileName)
	if err != nil {
		return err
	}
	result, err := clientconfig.Install(clientconfig.Client{Key: client}, clientconfig.Scope{}, p, "")
	if err == nil {
		for _, f := range result.Files {
			fmt.Fprintf(os.Stderr, "camp: updated %s\n", f)
		}
	}
	return err
}

// setupStorage rewrites the storage section: local directory (default) or
// S3 with endpoint/bucket/region and a credentials reference. Validation
// failure preserves the previous working configuration.
func setupStorage(args []string) error {
	flags := flag.NewFlagSet("setup storage", flag.ContinueOnError)
	mode := flags.String("mode", "file", "storage mode: file or s3")
	endpoint := flags.String("endpoint", "", "S3 endpoint URL")
	bucket := flags.String("bucket", "", "S3 bucket")
	region := flags.String("region", "us-east-1", "S3 region")
	credentialsRef := flags.String("credentials-ref", "", "reference to a credential in private storage")
	accessKey := flags.String("access-key", "", "S3 access key (stored privately, never in the config)")
	secretKey := flags.String("secret-key", "", "S3 secret key (stored privately, never in the config)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	path := configPath()
	cfg, err := config.Load(path, config.Overrides{})
	if err != nil {
		return err
	}
	cfg.Storage.Mode = *mode
	cfg.Storage.Endpoint, cfg.Storage.Bucket, cfg.Storage.Region = *endpoint, *bucket, *region
	cfg.Storage.CredentialsRef = *credentialsRef
	cfg.Storage.AccessKey, cfg.Storage.SecretKey = *accessKey, *secretKey
	if err := config.Save(path, cfg); err != nil {
		return fmt.Errorf("keeping previous configuration: %w", err)
	}
	if *mode == "s3" && *accessKey != "" {
		store := clientconfig.Store{Dir: clientconfig.DefaultStoreDir()}
		if err := store.SaveCredential(*credentialsRef, *accessKey+":"+*secretKey); err != nil {
			return err
		}
	}
	fmt.Println("Storage configuration saved (atomic; secrets stored separately).")
	return nil
}

// setupAgents revisits client integration only.
func setupAgents(args []string) error {
	flags := flag.NewFlagSet("setup agents", flag.ContinueOnError)
	clients := flags.String("clients", "", "comma-separated clients")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *clients == "" {
		return fmt.Errorf("usage: camp setup agents --clients claude-code,codex")
	}
	return setupCommand([]string{"--clients", *clients, "--service=false"})
}

// setupServer prepares a server-only installation (no desktop clients):
// configuration, control state, and the starter workspace. The
// administrator credential is stored privately.
func setupServer(args []string) error {
	flags := flag.NewFlagSet("setup server", flag.ContinueOnError)
	open := flags.Bool("open-registration", false, "allow self-registration on this server (explicit owner choice)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	path := configPath()
	cfg, err := config.Load(path, config.Overrides{})
	if err != nil {
		return err
	}
	if *open {
		cfg.Registration.Mode = "open"
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataRoot, 0o700); err != nil {
		return err
	}
	// Initialize the instance: bootstrap the admin credential and the
	// starter workspace through the same engine path `camp init` uses.
	adminToken := makeToken()
	ctx := context.Background()
	engine, err := server.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()
	if err = engine.Bootstrap(ctx, adminToken, true); err != nil {
		return err
	}
	store := clientconfig.Store{Dir: clientconfig.DefaultStoreDir()}
	if err := store.SaveCredential("server-admin", adminToken); err != nil {
		return err
	}
	fmt.Printf("Server instance ready at %s (data under %s).\nAdmin credential saved privately (ref server-admin).\nRun: camp serve\n", cfg.Listen, cfg.DataRoot)
	return nil
}

func makeToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
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
	service := localservice.Service{Dir: cfg.DataRoot, Command: "", ConfigPath: configPath()}
	pid, err := service.Running()
	running := pid != 0 && healthPing(cfg.Listen)
	fmt.Printf("profile: %s\nserver: %s\nstate: %s\n", configPath(), cfg.Listen, map[bool]string{true: "running", false: "stopped"}[running])
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
	launcher, err := absoluteLauncher()
	if err != nil {
		return err
	}
	service := localservice.Service{Dir: cfg.DataRoot, Command: launcher, ConfigPath: configPath()}
	pid, err := service.Start(cfg.Listen)
	if err != nil {
		return err
	}
	fmt.Printf("camp server running (pid %d) on %s\n", pid, cfg.Listen)
	return nil
}

func downCommand(args []string) error {
	cfg, err := loadConfig(config.Overrides{})
	if err != nil {
		return err
	}
	service := localservice.Service{Dir: cfg.DataRoot, Command: "", ConfigPath: configPath()}
	if err := service.Stop(); err != nil {
		return err
	}
	fmt.Println("camp server stopped; data preserved")
	return nil
}
