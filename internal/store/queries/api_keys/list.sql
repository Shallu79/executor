SELECT id, user_id, name, prefix, key_hash, revoked, last_used_at, created_at
  FROM api_keys
 WHERE user_id = $1
 ORDER BY created_at DESC
