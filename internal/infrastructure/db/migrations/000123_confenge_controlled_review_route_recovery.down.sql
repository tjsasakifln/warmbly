-- Deliberately irreversible data repair. Re-blocking a later authoritative
-- controlled route on rollback would overwrite newer upstream truth.
SELECT 1;
