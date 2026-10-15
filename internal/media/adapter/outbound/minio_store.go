package outbound

import "context"

// MinIOStore — TODO: реальный S3-совместимый клиент (MinIO локально,
// CLAUDE.md §6).
type MinIOStore struct {
	endpoint string
}

func NewMinIOStore(endpoint string) *MinIOStore {
	return &MinIOStore{endpoint: endpoint}
}

func (s *MinIOStore) Put(ctx context.Context, key string, data []byte) error {
	return nil
}
