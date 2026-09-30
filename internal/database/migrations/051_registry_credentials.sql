CREATE TABLE IF NOT EXISTS registry_credentials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    registry VARCHAR(100) NOT NULL UNIQUE,
    username VARCHAR(255) NOT NULL,
    secret_ciphertext BYTEA NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT registry_credentials_registry CHECK (registry IN ('docker.io', 'ghcr.io'))
);
