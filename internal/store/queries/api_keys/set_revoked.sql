UPDATE api_keys
   SET revoked = $1
 WHERE id = $2 AND user_id = $3
