// Package sshclient provides the SSH client producer.
package sshclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/database64128/ddns-go/jsoncfg"
	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/poller"
	"github.com/database64128/ddns-go/tslog"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SourceConfig contains configuration options for the SSH client IP address source.
type SourceConfig struct {
	// Command4 is the command to execute on the remote host to obtain the IPv4 address.
	Command4 string

	// Command6 is the command to execute on the remote host to obtain the IPv6 address.
	Command6 string

	// Network is the network type to use for the SSH connection (e.g., "tcp", "tcp4", "tcp6").
	Network string

	// Address is the address of the SSH server (e.g., "example.com:22").
	Address string

	// KeyExchanges is assigned to [ssh.Config.KeyExchanges].
	//
	// Leave empty to use the default.
	KeyExchanges []string

	// Ciphers is assigned to [ssh.Config.Ciphers].
	//
	// Leave empty to use the default.
	Ciphers []string

	// MACs is assigned to [ssh.Config.MACs].
	//
	// Leave empty to use the default.
	MACs []string

	// User is the username for SSH authentication.
	User string

	// Auth is the list of authentication methods for SSH.
	Auth []ssh.AuthMethod

	// HostKeyCallback is the callback function for verifying the server's host key.
	HostKeyCallback ssh.HostKeyCallback

	// HostKeyAlgorithms is assigned to [ssh.ClientConfig.HostKeyAlgorithms].
	//
	// As of OpenSSH 10.3, you'll want to set this to [ssh.KeyAlgoED25519] if you're
	// going to use OpenSSH's known_hosts file for host key verification.
	//
	// Leave empty to use the default.
	HostKeyAlgorithms []string
}

// NewSource returns a new SSH client IP address source.
func (cfg *SourceConfig) NewSource() *Source {
	return &Source{
		cmd4:              cfg.Command4,
		cmd6:              cfg.Command6,
		network:           cfg.Network,
		address:           cfg.Address,
		keyExchanges:      cfg.KeyExchanges,
		ciphers:           cfg.Ciphers,
		macs:              cfg.MACs,
		user:              cfg.User,
		auth:              cfg.Auth,
		hostKeyCallback:   cfg.HostKeyCallback,
		hostKeyAlgorithms: cfg.HostKeyAlgorithms,
	}
}

// Source obtains the remote host's IPv4 and/or IPv6 address by executing commands over SSH.
//
// Source implements [producer.Source].
type Source struct {
	client *ssh.Client

	// We don't ever call netConn.Close() directly; instead, we let the SSH client manage its lifetime.
	//
	// We don't ever set netConn to nil, because it's unnecessary, and it could easily lead to a data
	// race with the goroutine spawned by context.AfterFunc.
	netConn net.Conn

	stdoutBuffer bytes.Buffer
	stderrBuffer bytes.Buffer

	cmd4              string
	cmd6              string
	network           string
	address           string
	keyExchanges      []string
	ciphers           []string
	macs              []string
	user              string
	auth              []ssh.AuthMethod
	hostKeyCallback   ssh.HostKeyCallback
	hostKeyAlgorithms []string
}

// Snapshot returns the IPv4 and/or IPv6 address obtained from the remote host.
//
// Snapshot implements [producer.Source.Snapshot].
func (s *Source) Snapshot(ctx context.Context) (producer.Message, error) {
	if s.client == nil {
		var dialer net.Dialer
		nc, err := dialer.DialContext(ctx, s.network, s.address)
		if err != nil {
			return producer.Message{}, fmt.Errorf("failed to connect to SSH server: %w", err)
		}
		s.netConn = nc
	}

	stop := context.AfterFunc(ctx, func() {
		_ = s.netConn.SetDeadline(aLongTimeAgo)
	})
	defer func() {
		if !stop() {
			s.resetClient()
		}
	}()

	if s.client == nil {
		c, chans, req, err := ssh.NewClientConn(s.netConn, s.address, &ssh.ClientConfig{
			Config: ssh.Config{
				KeyExchanges: s.keyExchanges,
				Ciphers:      s.ciphers,
				MACs:         s.macs,
			},
			User:              s.user,
			Auth:              s.auth,
			HostKeyCallback:   s.hostKeyCallback,
			HostKeyAlgorithms: s.hostKeyAlgorithms,
		})
		if err != nil {
			// On error, ssh.NewClientConn closes the connection for you.
			return producer.Message{}, fmt.Errorf("failed to establish SSH connection: %w", err)
		}
		s.client = ssh.NewClient(c, chans, req)
	}

	var msg producer.Message

	if s.cmd4 != "" {
		addr, err := s.sshRun(s.cmd4)
		if err != nil {
			return producer.Message{}, err
		}
		if !addr.Is4() {
			return producer.Message{}, fmt.Errorf("not an IPv4 address: %s", addr)
		}
		msg.IPv4 = addr
	}

	if s.cmd6 != "" {
		addr, err := s.sshRun(s.cmd6)
		if err != nil {
			return producer.Message{}, err
		}
		if !addr.Is6() {
			return producer.Message{}, fmt.Errorf("not an IPv6 address: %s", addr)
		}
		msg.IPv6 = addr
	}

	return msg, nil
}

func (s *Source) sshRun(cmd string) (netip.Addr, error) {
	session, err := s.client.NewSession()
	if err != nil {
		s.resetClient()
		return netip.Addr{}, fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	session.Stdout = &s.stdoutBuffer
	session.Stderr = &s.stderrBuffer
	defer func() {
		s.stdoutBuffer.Reset()
		s.stderrBuffer.Reset()
	}()

	err = session.Run(cmd)
	stdout := s.stdoutBuffer.Bytes()
	stderr := s.stderrBuffer.Bytes()

	if err != nil {
		if _, ok := errors.AsType[*net.OpError](err); ok {
			s.resetClient()
		}
		return netip.Addr{}, fmt.Errorf("failed to run command: %w, stdout: %q, stderr: %q", err, stdout, stderr)
	}

	stdout = bytes.TrimSpace(stdout)

	addr, err := netip.ParseAddr(string(stdout))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("failed to parse IP address: %w", err)
	}
	return addr.Unmap(), nil
}

// aLongTimeAgo is a non-zero time, far in the past, used for immediate deadlines.
var aLongTimeAgo = time.Unix(0, 0)

func (s *Source) resetClient() {
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
}

// ProducerConfig contains configuration options for the SSH client producer.
type ProducerConfig struct {
	// Command4 is the command to execute on the remote host to obtain the IPv4 address.
	Command4 string `json:"command4"`

	// Command6 is the command to execute on the remote host to obtain the IPv6 address.
	Command6 string `json:"command6"`

	// Network is the network type to use for the SSH connection (e.g., "tcp", "tcp4", "tcp6").
	Network string `json:"network"`

	// Address is the address of the SSH server (e.g., "example.com:22").
	Address string `json:"address"`

	// KeyExchanges is assigned to [ssh.Config.KeyExchanges].
	//
	// Leave empty to use the default.
	KeyExchanges []string `json:"key_exchanges,omitzero"`

	// Ciphers is assigned to [ssh.Config.Ciphers].
	//
	// Leave empty to use the default.
	Ciphers []string `json:"ciphers,omitzero"`

	// MACs is assigned to [ssh.Config.MACs].
	//
	// Leave empty to use the default.
	MACs []string `json:"macs,omitzero"`

	// User is the username for SSH authentication.
	User string `json:"user"`

	// Keys is the list of SSH key configurations for authentication.
	Keys []SSHKeyConfig `json:"keys,omitzero"`

	// IdentityAgent is the path to the SSH agent socket for authentication.
	//
	// Instances of ${var} and $var are expanded using the current environment variables.
	//
	// The special value "none" disables the use of the SSH agent.
	//
	// If empty, $SSH_AUTH_SOCK is used if it is set.
	IdentityAgent string `json:"identity_agent,omitzero"`

	// HostPublicKeyPath is the path to the SSH server's host public key file for host key verification.
	//
	// If empty, the OpenSSH known_hosts files are used.
	HostPublicKeyPath string `json:"host_public_key_path,omitzero"`

	// KnownHostsPaths is the list of paths to the OpenSSH known_hosts file for host key verification.
	//
	// If empty, it defaults to ["~/.ssh/known_hosts"].
	KnownHostsPaths []string `json:"known_hosts_paths,omitzero"`

	// HostKeyAlgorithms is assigned to [ssh.ClientConfig.HostKeyAlgorithms].
	//
	// As of OpenSSH 10.3, you'll want to set this to [ssh.KeyAlgoED25519] if you're
	// going to use OpenSSH's known_hosts file for host key verification.
	//
	// Leave empty to use the default.
	HostKeyAlgorithms []string `json:"host_key_algorithms,omitzero"`

	// PollInterval is the interval between command executions.
	// If not positive, it defaults to 5 minutes.
	PollInterval jsoncfg.Duration `json:"poll_interval,omitzero"`
}

// NewProducer creates a new [producer.Producer] that monitors the remote host's IPv4 and/or IPv6 address via SSH.
func (cfg *ProducerConfig) NewProducer(logger *tslog.Logger) (producer.Producer, error) {
	var keyAuth ssh.AuthMethod
	if len(cfg.Keys) > 0 {
		signers := make([]ssh.Signer, len(cfg.Keys))
		for i := range cfg.Keys {
			signer, err := cfg.Keys[i].Signer()
			if err != nil {
				return nil, fmt.Errorf("failed to create signer for keys[%d]: %w", i, err)
			}
			signers[i] = signer
		}
		keyAuth = ssh.PublicKeys(signers...)
	}

	var agentAuth ssh.AuthMethod
	if cfg.IdentityAgent != "none" {
		var agentPath string
		if cfg.IdentityAgent != "" {
			agentPath = os.ExpandEnv(cfg.IdentityAgent)
		} else {
			agentPath = os.Getenv("SSH_AUTH_SOCK")
		}

		if agentPath != "" {
			agentAuth = ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
				conn, err := net.Dial("unix", agentPath)
				if err != nil {
					return nil, fmt.Errorf("failed to connect to SSH agent at %q: %w", agentPath, err)
				}
				defer conn.Close()

				agentClient := agent.NewClient(conn)
				signers, err := agentClient.Signers()
				if err != nil {
					return nil, fmt.Errorf("failed to get signers from SSH agent: %w", err)
				}
				return signers, nil
			})
		}
	}

	var authMethodCount int
	if keyAuth != nil {
		authMethodCount++
	}
	if agentAuth != nil {
		authMethodCount++
	}
	if authMethodCount == 0 {
		return nil, errors.New("no authentication methods provided; please specify SSH keys or an identity agent")
	}

	authMethods := make([]ssh.AuthMethod, 0, authMethodCount)
	if keyAuth != nil {
		authMethods = append(authMethods, keyAuth)
	}
	if agentAuth != nil {
		authMethods = append(authMethods, agentAuth)
	}

	var hostKeyCallback ssh.HostKeyCallback
	if cfg.HostPublicKeyPath != "" {
		pubKeyBytes, err := os.ReadFile(cfg.HostPublicKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read host public key file %q: %w", cfg.HostPublicKeyPath, err)
		}

		pubKey, err := ssh.ParsePublicKey(pubKeyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse host public key: %w", err)
		}

		hostKeyCallback = ssh.FixedHostKey(pubKey)
	} else {
		knownHostsPaths := cfg.KnownHostsPaths
		if len(knownHostsPaths) == 0 {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get user home directory: %w", err)
			}
			knownHostsPaths = []string{homeDir + "/.ssh/known_hosts"}
		}

		knownHostsCallback, err := knownhosts.New(knownHostsPaths...)
		if err != nil {
			return nil, fmt.Errorf("failed to create known_hosts callback: %w", err)
		}
		hostKeyCallback = knownHostsCallback
	}

	// Remove this workaround once https://go-review.googlesource.com/c/crypto/+/800080
	// is merged and released.
	var (
		keyExchanges      []string
		ciphers           []string
		macs              []string
		hostKeyAlgorithms []string
	)
	if len(cfg.KeyExchanges) > 0 {
		keyExchanges = cfg.KeyExchanges
	}
	if len(cfg.Ciphers) > 0 {
		ciphers = cfg.Ciphers
	}
	if len(cfg.MACs) > 0 {
		macs = cfg.MACs
	}
	if len(cfg.HostKeyAlgorithms) > 0 {
		hostKeyAlgorithms = cfg.HostKeyAlgorithms
	}

	srcCfg := SourceConfig{
		Command4:          cfg.Command4,
		Command6:          cfg.Command6,
		Network:           cfg.Network,
		Address:           cfg.Address,
		KeyExchanges:      keyExchanges,
		Ciphers:           ciphers,
		MACs:              macs,
		User:              cfg.User,
		Auth:              authMethods,
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: hostKeyAlgorithms,
	}
	source := srcCfg.NewSource()

	pollInterval := cfg.PollInterval.Value()
	if pollInterval <= 0 {
		pollInterval = 5 * time.Minute
	}

	return poller.New(pollInterval, source, logger), nil
}

// SSHKeyConfig contains configuration options for an SSH key.
type SSHKeyConfig struct {
	// PrivateKeyPath specifies the path to the private key file.
	PrivateKeyPath string `json:"private_key_path"`

	// PrivateKeyPassphrase specifies the passphrase for the private key, if it has one.
	PrivateKeyPassphrase string `json:"private_key_passphrase,omitzero"`

	// CertificatePath specifies the path to the user certificate file, if using certificate-based authentication.
	CertificatePath string `json:"certificate_path,omitzero"`
}

// Signer returns an [ssh.Signer] for the SSH key configuration, which can be used for authentication.
func (cfg *SSHKeyConfig) Signer() (ssh.Signer, error) {
	pemBytes, err := os.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file %q: %w", cfg.PrivateKeyPath, err)
	}

	var signer ssh.Signer
	if cfg.PrivateKeyPassphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(cfg.PrivateKeyPassphrase))
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key with passphrase: %w", err)
		}
	} else {
		signer, err = ssh.ParsePrivateKey(pemBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
	}

	if cfg.CertificatePath != "" {
		certBytes, err := os.ReadFile(cfg.CertificatePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read certificate file %q: %w", cfg.CertificatePath, err)
		}

		pubKey, err := ssh.ParsePublicKey(certBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse certificate: %w", err)
		}

		cert, ok := pubKey.(*ssh.Certificate)
		if !ok {
			return nil, fmt.Errorf("not an SSH certificate: %q", cfg.CertificatePath)
		}

		signer, err = ssh.NewCertSigner(cert, signer)
		if err != nil {
			return nil, fmt.Errorf("failed to create cert signer: %w", err)
		}
	}

	return signer, nil
}
