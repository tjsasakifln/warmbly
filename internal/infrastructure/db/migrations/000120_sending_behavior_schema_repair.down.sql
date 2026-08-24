-- Deliberately irreversible: migration 120 repairs objects owned by migration
-- 81, so rolling it back must not remove the canonical sending schema.
SELECT 1;
