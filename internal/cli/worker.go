package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/prasenjeet-symon/ogcode/internal/worker"
	"github.com/spf13/cobra"
)

var (
	workerMaster     string
	workerName       string
	workerWorkspaces []string
	workerRepoRoot   string
	workerSecretFile string
	workerMasterCA   string
	workerInsecure   bool
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Run ogcode as a remote worker for a control plane",
	Long: `Run this ogcode as a long-lived worker that hosts agent sessions on
behalf of a remote control plane (the separate ogcode-control-plane service).

The worker dials the control plane over ConnectRPC, authenticates with a shared
pairing secret, and opens a single stream over which the control plane starts and
stops sessions in this machine's workspaces. Each session runs on a full ogcode
server for its worktree, reachable from the panel through an outbound-only
tunnel — the worker listens on no inbound port of its own.

The pairing secret is read from $OGCODE_PAIRING_SECRET (or --pairing-secret-file)
so it never appears in the process arguments.`,
	Example: `  OGCODE_PAIRING_SECRET=… ogcode worker --master https://master:8443 --workspace ~/code/app`,
	RunE:    runWorker,
}

func init() {
	workerCmd.Flags().StringVar(&workerMaster, "master", "", "control-plane URL, e.g. https://master:8443 (required)")
	workerCmd.Flags().StringVar(&workerName, "name", "", "worker display name (default: hostname)")
	workerCmd.Flags().StringArrayVar(&workerWorkspaces, "workspace", nil, "workspace root to offer (repeatable; defaults to the current directory)")
	workerCmd.Flags().StringVar(&workerRepoRoot, "repo-root", "", "directory for managed repo clones, i.e. multi-user repo assignment (default ~/.ogcode/repos; pass it also as --workspace so clones are hostable)")
	workerCmd.Flags().StringVar(&workerSecretFile, "pairing-secret-file", "", "file holding the pairing secret (else $OGCODE_PAIRING_SECRET)")
	workerCmd.Flags().StringVar(&workerMasterCA, "master-ca", "", "PEM file of CA(s) to trust for the master's TLS cert (for a self-signed / private-CA master)")
	workerCmd.Flags().BoolVar(&workerInsecure, "insecure", false, "skip TLS verification of the master (development only)")
	rootCmd.AddCommand(workerCmd)
}

func runWorker(cmd *cobra.Command, args []string) error {
	if workerMaster == "" {
		return fmt.Errorf("--master is required (e.g. https://master:8443)")
	}

	secret := os.Getenv("OGCODE_PAIRING_SECRET")
	if workerSecretFile != "" {
		b, err := os.ReadFile(workerSecretFile)
		if err != nil {
			return fmt.Errorf("read pairing secret file: %w", err)
		}
		secret = strings.TrimSpace(string(b))
	}
	if secret == "" {
		return fmt.Errorf("pairing secret required — set OGCODE_PAIRING_SECRET or --pairing-secret-file")
	}

	workspaces := workerWorkspaces
	if len(workspaces) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			workspaces = []string{cwd}
		}
	}

	w := worker.New(worker.Options{
		MasterURL:     workerMaster,
		PairingSecret: secret,
		WorkerName:    workerName,
		Workspaces:    workspaces,
		RepoRoot:      workerRepoRoot,
		CACertPath:    workerMasterCA,
		Insecure:      workerInsecure,
		Logger:        slog.Default(),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("ogcode worker starting", "master", workerMaster, "workspaces", workspaces)
	if err := w.Run(ctx); err != nil {
		return err
	}
	slog.Info("ogcode worker stopped")
	return nil
}
