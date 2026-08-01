UPDATE api_keys
   SET last_used_at = now()
 WHERE id = $1
