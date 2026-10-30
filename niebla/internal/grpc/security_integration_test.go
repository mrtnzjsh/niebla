package grpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"niebla/internal/cli"
	"niebla/internal/config"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// generateTestCertificates creates a temporary CA certificate and a server certificate/key pair
// signed by the CA, valid for 127.0.0.1 and localhost.
func generateTestCertificates(t *testing.T, dir string) (caFile, certFile, keyFile string) {
	t.Helper()

	// 1. Generate Root CA Key and Certificate
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "Niebla Test CA",
			Organization: []string{"Niebla Security Tests"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}

	caFile = filepath.Join(dir, "ca.crt")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := os.WriteFile(caFile, caPEM, 0644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	// 2. Generate Server Key and Certificate signed by CA
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server certificate: %v", err)
	}

	certFile = filepath.Join(dir, "server.crt")
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	if err := os.WriteFile(certFile, serverPEM, 0644); err != nil {
		t.Fatalf("failed to write server cert file: %v", err)
	}

	keyFile = filepath.Join(dir, "server.key")
	serverKeyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatalf("failed to marshal server key: %v", err)
	}
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyBytes})
	if err := os.WriteFile(keyFile, serverKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write server key file: %v", err)
	}

	return caFile, certFile, keyFile
}

func TestSecurity_TLSTransportAndBearerAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	// 1. Setup Database & CAS via existing test helper
	_, database, _, repoServer, dbCleanup := setupTestEnvironment(t)
	defer dbCleanup()

	// 2. Generate TLS certificates in temp directory
	certDir := t.TempDir()
	caFile, certFile, keyFile := generateTestCertificates(t, certDir)

	// 3. Build Authenticator with allowed tokens
	validToken := "super-secret-agent-token-xyz"
	auth := NewAuthenticator([]string{validToken})

	// 4. Start TLS gRPC Server with auth interceptors
	serverCreds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
	if err != nil {
		t.Fatalf("failed to create server TLS credentials: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.Creds(serverCreds),
		grpc.UnaryInterceptor(auth.UnaryServerInterceptor()),
		grpc.StreamInterceptor(auth.StreamServerInterceptor()),
	)
	repoServer.Register(grpcServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on random port: %v", err)
	}
	serverAddr := listener.Addr().String()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	// Create a test CL in DB for GetCL tests
	ctx := context.Background()
	testCLID, err := database.CreateShelvedCL(ctx, "carol", "test cl", []byte{1, 2, 3}, 0)
	if err != nil {
		t.Fatalf("failed to create test CL: %v", err)
	}

	// --- Case 1: Plaintext connection to TLS server is rejected ---
	t.Run("plaintext_rejected_by_tls_server", func(t *testing.T) {
		insecureConn, err := grpc.DialContext(
			ctx,
			serverAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithTimeout(500*time.Millisecond),
		)
		if err == nil {
			client := nieblav1.NewNieblaServiceClient(insecureConn)
			_, err = client.GetCL(ctx, &nieblav1.GetCLRequest{ClId: testCLID})
			insecureConn.Close()
			if err == nil {
				t.Fatalf("expected plaintext RPC to fail on TLS server, but succeeded")
			}
		}
	})

	// --- Case 2: TLS connection without Bearer token -> Unauthenticated ---
	t.Run("missing_token_unauthenticated", func(t *testing.T) {
		tlsCfg := &config.ClientTLSConfig{
			Enabled:    true,
			CAFile:     caFile,
			ServerName: "127.0.0.1",
		}
		conn, client, err := cli.DialGRPC(ctx, serverAddr, cli.WithTLSConfig(tlsCfg))
		if err != nil {
			t.Fatalf("failed to dial TLS server: %v", err)
		}
		defer conn.Close()

		_, err = client.GetCL(ctx, &nieblav1.GetCLRequest{ClId: testCLID})
		if err == nil {
			t.Fatalf("expected Unauthenticated error for missing token, got success")
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	// --- Case 3: TLS connection with invalid Bearer token -> Unauthenticated ---
	t.Run("invalid_token_unauthenticated", func(t *testing.T) {
		tlsCfg := &config.ClientTLSConfig{
			Enabled:    true,
			CAFile:     caFile,
			ServerName: "127.0.0.1",
		}
		conn, client, err := cli.DialGRPC(ctx, serverAddr, cli.WithTLSConfig(tlsCfg), cli.WithToken("wrong-token"))
		if err != nil {
			t.Fatalf("failed to dial TLS server: %v", err)
		}
		defer conn.Close()

		_, err = client.GetCL(ctx, &nieblav1.GetCLRequest{ClId: testCLID})
		if err == nil {
			t.Fatalf("expected Unauthenticated error for wrong token, got success")
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("expected code Unauthenticated, got %v", status.Code(err))
		}
	})

	// --- Case 4: TLS connection with valid token -> Unary RPC succeeds ---
	t.Run("valid_token_unary_success", func(t *testing.T) {
		tlsCfg := &config.ClientTLSConfig{
			Enabled:    true,
			CAFile:     caFile,
			ServerName: "127.0.0.1",
		}
		conn, client, err := cli.DialGRPC(ctx, serverAddr, cli.WithTLSConfig(tlsCfg), cli.WithToken(validToken))
		if err != nil {
			t.Fatalf("failed to dial TLS server: %v", err)
		}
		defer conn.Close()

		resp, err := client.GetCL(ctx, &nieblav1.GetCLRequest{ClId: testCLID})
		if err != nil {
			t.Fatalf("expected successful GetCL with valid token, got: %v", err)
		}
		if resp.ClId != testCLID {
			t.Errorf("expected CL ID %d, got %d", testCLID, resp.ClId)
		}
	})

	// --- Case 5: TLS connection with valid token -> Streaming RPC succeeds ---
	t.Run("valid_token_streaming_success", func(t *testing.T) {
		tlsCfg := &config.ClientTLSConfig{
			Enabled:    true,
			CAFile:     caFile,
			ServerName: "127.0.0.1",
		}
		conn, client, err := cli.DialGRPC(ctx, serverAddr, cli.WithTLSConfig(tlsCfg), cli.WithToken(validToken))
		if err != nil {
			t.Fatalf("failed to dial TLS server: %v", err)
		}
		defer conn.Close()

		stream, err := client.UploadBlob(ctx)
		if err != nil {
			t.Fatalf("failed to open UploadBlob stream: %v", err)
		}

		// Send a small chunk
		chunk := []byte("secure stream content over TLS with bearer auth")
		err = stream.Send(&nieblav1.UploadBlobRequest{
			Data: chunk,
		})
		if err != nil && err != io.EOF {
			t.Fatalf("failed to send chunk: %v", err)
		}

		resp, err := stream.CloseAndRecv()
		if err != nil {
			t.Fatalf("stream CloseAndRecv failed: %v", err)
		}
		if len(resp.Sha256) == 0 {
			t.Errorf("expected non-empty SHA256 response from UploadBlob")
		}
	})
}
