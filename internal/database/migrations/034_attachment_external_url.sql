-- 034_attachment_external_url.sql
-- External-URL handouts. Big static handouts (e.g. a 662MB forensics memory image)
-- live on the public GCS bucket, not in Anvil's storage backend (which caps ~32MB).
-- An attachment row may now carry a `url` instead of a stored object: the public
-- download endpoint 302-redirects to it, so the challenge page's existing download
-- button works unchanged. `sha256` lets players verify a large download. storage_key
-- becomes nullable (an external attachment has no stored object; UNIQUE still allows
-- multiple NULLs in Postgres).
ALTER TABLE challenge_attachments ADD COLUMN IF NOT EXISTS url TEXT;
ALTER TABLE challenge_attachments ADD COLUMN IF NOT EXISTS sha256 VARCHAR(64);
ALTER TABLE challenge_attachments ALTER COLUMN storage_key DROP NOT NULL;
