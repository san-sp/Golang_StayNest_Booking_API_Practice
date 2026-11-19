```
LOGIN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "staynest.test@example.com",
    "password": "StayNestTest@123"
  }')

echo "$LOGIN" | jq
```

```
TOKEN=$(echo "$LOGIN" | jq -r '.access_token')

echo "Token length: ${#TOKEN}"
```

```
find . -name "*.go" | grep -v "_test.go" | xargs wc -l
```
