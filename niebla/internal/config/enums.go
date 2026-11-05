package config

import "strings"

// AuthMode represents the user authentication and identity resolution strategy.
type AuthMode string

const (
	AuthModeLocal     AuthMode = "local"
	AuthModeHeaders   AuthMode = "headers"
	AuthModeTailscale AuthMode = "tailscale"
)

var AllAuthModes = []AuthMode{
	AuthModeLocal,
	AuthModeHeaders,
	AuthModeTailscale,
}

func (m AuthMode) IsValid() bool {
	switch m {
	case AuthModeLocal, AuthModeHeaders, AuthModeTailscale:
		return true
	default:
		return false
	}
}

func (m AuthMode) String() string {
	return string(m)
}

// GroupMode specifies how user groups and team memberships are resolved.
type GroupMode string

const (
	GroupModeStatic   GroupMode = "static"
	GroupModeAPI      GroupMode = "api"
	GroupModeDisabled GroupMode = "disabled"
)

var AllGroupModes = []GroupMode{
	GroupModeStatic,
	GroupModeAPI,
	GroupModeDisabled,
}

func (m GroupMode) IsValid() bool {
	switch m {
	case GroupModeStatic, GroupModeAPI, GroupModeDisabled:
		return true
	default:
		return false
	}
}

func (m GroupMode) String() string {
	return string(m)
}

// EventSinkType represents the delivery mechanism for published toolchain events.
type EventSinkType string

const (
	EventSinkWebhook EventSinkType = "webhook"
	EventSinkExec    EventSinkType = "exec"
	EventSinkLog     EventSinkType = "log"
	EventSinkNoop    EventSinkType = "noop"
)

var AllEventSinkTypes = []EventSinkType{
	EventSinkWebhook,
	EventSinkExec,
	EventSinkLog,
	EventSinkNoop,
}

func (s EventSinkType) IsValid() bool {
	switch s {
	case EventSinkWebhook, EventSinkExec, EventSinkLog, EventSinkNoop:
		return true
	default:
		return false
	}
}

func (s EventSinkType) String() string {
	return string(s)
}

// ResilienceMode specifies the disaster recovery and synchronization driver.
type ResilienceMode string

const (
	ResilienceModeZFSStream ResilienceMode = "zfs_stream"
	ResilienceModeBundle    ResilienceMode = "bundle"
)

var AllResilienceModes = []ResilienceMode{
	ResilienceModeZFSStream,
	ResilienceModeBundle,
}

func (m ResilienceMode) IsValid() bool {
	switch m {
	case ResilienceModeZFSStream, ResilienceModeBundle:
		return true
	default:
		return false
	}
}

func (m ResilienceMode) String() string {
	return string(m)
}

// BackupTarget represents a storage target destination for snapshot bundles.
type BackupTarget string

const (
	BackupTargetZFS        BackupTarget = "zfs"
	BackupTargetS3         BackupTarget = "s3"
	BackupTargetSFTP       BackupTarget = "sftp"
	BackupTargetNextcloud  BackupTarget = "nextcloud"
	BackupTargetFilesystem BackupTarget = "filesystem"
	BackupTargetBundle     BackupTarget = "bundle"
)

var AllBackupTargets = []BackupTarget{
	BackupTargetZFS,
	BackupTargetS3,
	BackupTargetSFTP,
	BackupTargetNextcloud,
	BackupTargetFilesystem,
	BackupTargetBundle,
}

func (t BackupTarget) IsValid() bool {
	switch t {
	case BackupTargetZFS, BackupTargetS3, BackupTargetSFTP, BackupTargetNextcloud, BackupTargetFilesystem, BackupTargetBundle:
		return true
	default:
		return false
	}
}

func (t BackupTarget) String() string {
	return string(t)
}

// EncryptionAlgorithm specifies authenticated symmetric encryption at rest.
type EncryptionAlgorithm string

const (
	EncryptionAlgorithmAES256GCM         EncryptionAlgorithm = "aes-256-gcm"
	EncryptionAlgorithmChaCha20Poly1305  EncryptionAlgorithm = "chacha20-poly1305"
)

var AllEncryptionAlgorithms = []EncryptionAlgorithm{
	EncryptionAlgorithmAES256GCM,
	EncryptionAlgorithmChaCha20Poly1305,
}

func (a EncryptionAlgorithm) IsValid() bool {
	switch a {
	case EncryptionAlgorithmAES256GCM, EncryptionAlgorithmChaCha20Poly1305:
		return true
	default:
		return false
	}
}

func (a EncryptionAlgorithm) String() string {
	return string(a)
}

// KeySource specifies the credential origin for master encryption keys.
type KeySource string

const (
	KeySourceEnv        KeySource = "env"
	KeySourceFile       KeySource = "file"
	KeySourcePassphrase KeySource = "passphrase"
)

var AllKeySources = []KeySource{
	KeySourceEnv,
	KeySourceFile,
	KeySourcePassphrase,
}

func (k KeySource) IsValid() bool {
	switch k {
	case KeySourceEnv, KeySourceFile, KeySourcePassphrase:
		return true
	default:
		return false
	}
}

func (k KeySource) String() string {
	return string(k)
}

// CompressionFormat specifies compression encoding for backup archives.
type CompressionFormat string

const (
	CompressionFormatZstd CompressionFormat = "zstd"
	CompressionFormatGzip CompressionFormat = "gzip"
	CompressionFormatNone CompressionFormat = "none"
)

var AllCompressionFormats = []CompressionFormat{
	CompressionFormatZstd,
	CompressionFormatGzip,
	CompressionFormatNone,
}

func (c CompressionFormat) IsValid() bool {
	switch c {
	case CompressionFormatZstd, CompressionFormatGzip, CompressionFormatNone:
		return true
	default:
		return false
	}
}

func (c CompressionFormat) String() string {
	return string(c)
}

// CIProvider identifies a CI execution runner backend.
type CIProvider string

const (
	CIProviderLocal   CIProvider = "local"
	CIProviderForgejo CIProvider = "forgejo"
	CIProviderGitHub  CIProvider = "github"
	CIProviderWebhook CIProvider = "webhook"
)

var AllCIProviders = []CIProvider{
	CIProviderLocal,
	CIProviderForgejo,
	CIProviderGitHub,
	CIProviderWebhook,
}

func (p CIProvider) IsValid() bool {
	switch p {
	case CIProviderLocal, CIProviderForgejo, CIProviderGitHub, CIProviderWebhook:
		return true
	default:
		return false
	}
}

func (p CIProvider) String() string {
	return string(p)
}

// CIAuthMode specifies the authentication mechanism used to invoke remote CI runners.
type CIAuthMode string

const (
	CIAuthModeBearer CIAuthMode = "bearer"
	CIAuthModeToken  CIAuthMode = "token"
	CIAuthModeBasic  CIAuthMode = "basic"
	CIAuthModeHeader CIAuthMode = "header"
	CIAuthModeHMAC   CIAuthMode = "hmac"
	CIAuthModeNone   CIAuthMode = "none"
)

var AllCIAuthModes = []CIAuthMode{
	CIAuthModeBearer,
	CIAuthModeToken,
	CIAuthModeBasic,
	CIAuthModeHeader,
	CIAuthModeHMAC,
	CIAuthModeNone,
}

func (m CIAuthMode) IsValid() bool {
	switch m {
	case CIAuthModeBearer, CIAuthModeToken, CIAuthModeBasic, CIAuthModeHeader, CIAuthModeHMAC, CIAuthModeNone:
		return true
	default:
		return false
	}
}

func (m CIAuthMode) String() string {
	return string(m)
}

// PostgresReplicationMode specifies PostgreSQL secondary replication cadence.
type PostgresReplicationMode string

const (
	PostgresReplicationModeSnapshot   PostgresReplicationMode = "snapshot"
	PostgresReplicationModeContinuous PostgresReplicationMode = "continuous"
)

var AllPostgresReplicationModes = []PostgresReplicationMode{
	PostgresReplicationModeSnapshot,
	PostgresReplicationModeContinuous,
}

func (m PostgresReplicationMode) IsValid() bool {
	switch m {
	case PostgresReplicationModeSnapshot, PostgresReplicationModeContinuous:
		return true
	default:
		return false
	}
}

func (m PostgresReplicationMode) String() string {
	return string(m)
}

// NormalizeLower trims and lowercases an input string for enum parsing.
func NormalizeLower(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

