package httpclient

// AuthMode represents the authentication mechanism injected by the HTTP transport.
type AuthMode string

const (
	AuthModeNone   AuthMode = "none"
	AuthModeBearer AuthMode = "bearer"
	AuthModeToken  AuthMode = "token"
	AuthModeBasic  AuthMode = "basic"
	AuthModeHeader AuthMode = "header"
	AuthModeHMAC   AuthMode = "hmac"
)

// AllAuthModes lists all valid HTTP transport auth modes.
var AllAuthModes = []AuthMode{
	AuthModeNone,
	AuthModeBearer,
	AuthModeToken,
	AuthModeBasic,
	AuthModeHeader,
	AuthModeHMAC,
}

// IsValid reports whether the auth mode is recognized.
func (m AuthMode) IsValid() bool {
	switch m {
	case AuthModeNone, AuthModeBearer, AuthModeToken, AuthModeBasic, AuthModeHeader, AuthModeHMAC:
		return true
	default:
		return false
	}
}

func (m AuthMode) String() string {
	return string(m)
}

