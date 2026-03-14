CREATE TABLE IF NOT EXISTS uploaded_files (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL,
    file_name TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    sha256 CHAR(64) NOT NULL,
    storage_provider TEXT NOT NULL,
    storage_key TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_uploaded_files_owner_user_id ON uploaded_files(owner_user_id);
