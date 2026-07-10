package tfrun

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	meshcrypto "github.com/meshcloud/building-block-runner/go-meshapi-client/crypto"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	TMP_FILE_KNOWN_HOSTS = "known_hosts_tmp"
	TMP_FILE_SSH_CERT    = "ssh_cert"

	ERR_MSG_PREPARE                  = "An error occurred while preparing the SSH auth method. Please try again."
	ERR_MSG_INVALID_KEY              = "The provided SSH key is invalid. Please check the key and try again."
	ERR_MSG_KNOWN_HOSTS_FILE_BROKEN  = "The known_hosts file seems wrongly formatted. Please check the file and try again."
	ERR_MSG_KNOWN_HOSTS_NO_MATCH     = "No matching known_hosts entry found in the configuration for the target host and key. Please check the configured known_hosts and try again."
	ERR_MSG_KNOWN_HOST_NONE_PROVIDED = "Built in known_host entries do no contain a match for your host. Please add a known host entry to your configuration."
)

var ErrResolveGeneric = errors.New("ssh_error_generic")
var ErrResolveNoExtraKnownHost = errors.New("ssh_error_no_extra_known_host")

type SshAuth struct {
	knownHostEntry *KnownHost
	certStr        string
	prepDir        string
}

type KnownHost struct {
	host  string
	key   string
	value string
}

func (sshAuth *SshAuth) unwrapCert() (string, error) {
	// If crypto is not initialized (single-run mode), the cert is already decrypted
	if meshcrypto.Crypto == nil {
		return sshAuth.certStr, nil
	}
	// Otherwise, decrypt it (polling mode)
	r, e := meshcrypto.Crypto.DecryptMeshCertBased(sshAuth.certStr)
	return r, e
}

func (sshAuth *SshAuth) name() AuthType {
	return AUTH_TYPE_SSH
}

func (sshAuth *SshAuth) prepare(dir string, log *logwrap) error {
	log.PrintlnToLocalLogs(fmt.Sprintf("SshAuth perpare (dir '%s')", dir))

	sshAuth.prepDir = dir
	certPath := path.Join(dir, TMP_FILE_SSH_CERT)
	cert, err := sshAuth.unwrapCert()
	if err != nil {
		return err
	}

	// ensure trailing newline
	if !strings.HasSuffix(cert, "\n") {
		cert += "\n"
	}

	// persisting cert file
	log.PrintlnToLocalLogs(fmt.Sprintf("writing cert file to %s\n", certPath))
	err = os.WriteFile(certPath, []byte(cert), 0600)
	if err != nil {
		log.PrintlnToLocalLogs("unable to write cert file.")
		log.PrintlnToUpdateLogs(ERR_MSG_PREPARE)
		return err
	}

	// persisting known_hosts file
	if sshAuth.knownHostEntry != nil {
		khPath := path.Join(dir, TMP_FILE_KNOWN_HOSTS)
		log.PrintlnToLocalLogs(fmt.Sprintf("writing known_hosts to %s", khPath))
		err := os.WriteFile(khPath, []byte(sshAuth.knownHostEntry.toKnownHostsFileContent()), 0600)
		if err != nil {
			log.PrintlnToLocalLogs("unable to write known_hosts file.")
			log.PrintlnToUpdateLogs(ERR_MSG_PREPARE)
			return err
		}
	}

	return nil
}

func (sshAuth *SshAuth) done() error {
	// no need to clean up anything, files are in the working dir that will be removed anyway after the run
	return nil
}

func (sshAuth *SshAuth) toTransport(url string, log *logwrap) (transport.AuthMethod, error) {
	pemFile := path.Join(sshAuth.prepDir, TMP_FILE_SSH_CERT)

	log.PrintlnToLocalLogs(fmt.Sprintf("loading cert file from %s\n", pemFile))
	key, err := gogitssh.NewPublicKeysFromFile("git", pemFile, "")
	if err != nil {
		log.PrintlnToLocalLogs("could not parse cert file with private key.")
		log.PrintlnToUpdateLogs(ERR_MSG_INVALID_KEY)
		return nil, err
	}

	key.HostKeyCallbackHelper = sshAuth.knownHostsKeyCallback(sshAuth.prepDir, AppConfig.SkipHostKeyValidation, log)

	// Configure SSH client to request the specific key type from known_hosts
	// This tells the server which host key algorithm to use during handshake
	// Otherwise, the server may choose a different algorithm leading to host key mismatch
	// and a failed building block run.
	// TODO: maybe we should auto-discover known_hosts instead of letting users specify it?
	if sshAuth.knownHostEntry != nil && sshAuth.knownHostEntry.key != "" {
		log.PrintlnToLocalLogs(fmt.Sprintf("Configuring SSH client to use key type: %s", sshAuth.knownHostEntry.key))
		key.HostKeyAlgorithms = []string{sshAuth.knownHostEntry.key}
	}

	return key, err
}

func (kh *SshAuth) knownHostsKeyCallback(dir string, insecure bool, log *logwrap) gogitssh.HostKeyCallbackHelper {
	if insecure {
		log.PrintlnToLocalLogs("using insecure knownHostsKeyCallback!")
		return gogitssh.HostKeyCallbackHelper{
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
	}

	return kh.combinedKnownHostsKeyCallback(dir, log)
}

// this will return a function that uses a threefold approach of resolving hosts
// first: the KnownHostsHolder from an auth method
// second: we try the SSH_KNOWN_HOSTS environment variable
// third: we try the default: ~/.ssh/known_hosts
// if no method digs up a trusted host key, we give up.
func (sshAuth *SshAuth) combinedKnownHostsKeyCallback(dir string, log *logwrap) gogitssh.HostKeyCallbackHelper {
	log.PrintlnToLocalLogs("using various methods for known hosts")

	// this function will probably be called multiple times, using different available keys for this host
	combinedFunc := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// using known host from auth method
		msg := fmt.Sprintf("SSH attempting to access host '%s', remote addr '%s' with public key type '%s'.", hostname, remote.String(), key.Type())
		log.PrintlnToLocalLogs(msg)

		log.PrintlnToLocalLogs("method 1: trying configured known_host to find a trusted key")
		err := sshAuth.checkKnownHostsHolder(dir, hostname, remote, key, log)
		if err == nil {
			log.PrintlnToLocalLogs("method 1 succeeded")
			return nil
		}

		log.PrintlnToLocalLogs(fmt.Sprintf("method 1 failed:  %s\n", err.Error()))

		// using known host from environment variable
		msg = "method 2: trying known hosts from environment variable"
		log.PrintlnToLocalLogs(msg)
		errKnownHostsEnv := sshAuth.checkKnownHostsEnv(hostname, remote, key, log)
		if errKnownHostsEnv == nil {
			log.PrintlnToLocalLogs("method 2 succeeded")
			return nil
		}
		log.PrintlnToLocalLogs(fmt.Sprintf("method 2 failed:  %s\n", errKnownHostsEnv.Error()))

		// using known host from local known_hosts file
		msg = "method 3: trying local known_hosts file from runner"
		log.PrintlnToLocalLogs(msg)
		errLocalKnownHostFile := sshAuth.checkLocalKnownHostFile(hostname, remote, key, log)
		if errLocalKnownHostFile == nil {
			log.PrintlnToLocalLogs("method 3 succeeded")
			return nil
		}

		log.PrintlnToLocalLogs(
			fmt.Sprintf("method 3 failed:  %s\n", errLocalKnownHostFile.Error()),
			"ultimately failed SSH key discovery, all methods exhausted",
		)

		if sshAuth.knownHostEntry == nil {
			return ErrResolveNoExtraKnownHost
		} else {
			return ErrResolveGeneric
		}
	}

	return gogitssh.HostKeyCallbackHelper{HostKeyCallback: combinedFunc}
}

func (sshAuth *SshAuth) checkKnownHostsHolder(
	dir string,
	hostname string,
	remote net.Addr,
	key ssh.PublicKey,
	log *logwrap,
) error {
	// directly abort if no known host entry is defined
	if sshAuth.knownHostEntry == nil {
		msg := "no custom known_hosts defined"
		log.PrintlnToLocalLogs(msg)
		return errors.New(msg)
	}

	// load known hosts file
	knownHostHolderBasedCallback, err := knownhosts.New(path.Join(dir, TMP_FILE_KNOWN_HOSTS))
	if err != nil {
		log.PrintlnToLocalLogs(fmt.Sprintf("skip using known_host file, because of an error: '%s'\n", err.Error()))
		return err
	}

	// look up matching entry
	errCallback := knownHostHolderBasedCallback(hostname, remote, key)
	if errCallback == nil {
		return nil // success case
	} else {
		errMsg := fmt.Sprintf("could not find a valid entry for '%s', remote addr '%s' and public key type '%s', "+"due to following error: %s",
			hostname, remote.String(), key.Type(), errCallback.Error(),
		)
		log.PrintlnToLocalLogs(errMsg)
		return errors.New("could not find a valid entry")
	}
}

func (*SshAuth) checkKnownHostsEnv(hostname string, remote net.Addr, key ssh.PublicKey, log *logwrap) error {
	if len(os.Getenv("SSH_KNOWN_HOSTS")) == 0 {
		msg := "SSH_KNOWN_HOSTS not set"
		log.PrintlnToLocalLogs(msg)
		return errors.New(msg)
	}

	envKnownHostFile := os.Getenv("SSH_KNOWN_HOSTS")
	envBasedCallback, err := knownhosts.New(envKnownHostFile)

	if err != nil {
		log.PrintlnToLocalLogs(
			fmt.Sprintf("skip using SSH_KNOWN_HOSTS, because of an error: '%s'", err.Error()),
			"could not parse known_host file from environment",
		)
		return err
	}

	log.PrintlnToLocalLogs(fmt.Sprintf("using SSH_KNOWN_HOSTS file: '%s'", envKnownHostFile))

	errCallback := envBasedCallback(hostname, remote, key)
	if errCallback == nil {
		return nil
	} else {
		errMsg := fmt.Sprintf("failed using known_host from environment, due to following error: %s", errCallback.Error())
		log.PrintlnToLocalLogs(errMsg)
		return errors.New(errMsg)
	}
}

// method 3.
func (*SshAuth) checkLocalKnownHostFile(hostname string, remote net.Addr, key ssh.PublicKey, log *logwrap) error {
	knownHostsHome := filepath.Join(os.Getenv("HOME"), ".ssh", "known_hosts")
	localKnownHostsBasedCallback, err := knownhosts.New(knownHostsHome)

	if err != nil {
		log.PrintlnToLocalLogs("could not find global known_host file")
		return err
	}

	errCallback := localKnownHostsBasedCallback(hostname, remote, key)
	if errCallback == nil {
		return nil

	} else {
		msg := fmt.Sprintf("failed using global known_hosts file, due to following error: %s", errCallback.Error())
		log.PrintlnToLocalLogs(msg)
		return errors.New(msg)
	}
}

func (kh *KnownHost) toKnownHostsFileContent() string {
	return fmt.Sprintf("%s %s %s\n", kh.host, kh.key, kh.value)
}
