package photostore_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/photostore"
)

// TestS3Contract runs the shared store contract against a real MinIO server in a
// testcontainer, skipping under -short or when Docker is unavailable.
func TestS3Contract(t *testing.T) {
	if testing.Short() {
		t.Skip("s3 integration test needs MinIO; skipped with -short")
	}
	ctx := context.Background()
	setupCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	const user, pass = "minioadmin", "minioadmin"
	container, err := tcminio.Run(setupCtx, "minio/minio:RELEASE.2025-04-08T15-41-24Z",
		tcminio.WithUsername(user), tcminio.WithPassword(pass))
	if err != nil {
		if pgtest.IsDockerUnavailable(err) {
			t.Skipf("docker unavailable: %v", err)
		}
		t.Fatalf("start minio container: %v", err)
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate minio container: %v", err)
		}
	}()

	endpoint, err := container.ConnectionString(setupCtx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	s, err := photostore.NewS3(ctx, photostore.Config{
		Backend:   photostore.BackendS3,
		Endpoint:  endpoint,
		Bucket:    "thumbs",
		AccessKey: user,
		SecretKey: pass,
		Region:    "us-east-1",
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("new s3 store (bucket auto-created): %v", err)
	}
	storeContract(t, s)
}
