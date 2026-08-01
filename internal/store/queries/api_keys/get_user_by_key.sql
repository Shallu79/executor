SELECT u.id, u.email, u.password_hash, u.created_at,
       k.id, k.revoked
  FROM api_keys k
  JOIN users u ON u.id = k.user_id
 WHERE k.key_hash = $1
