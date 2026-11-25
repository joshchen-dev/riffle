# S3 archive

Bucket and write-only IAM for Riffle's archive of redacted matched lines.

- Private bucket: public access blocked, bucket-owner objects, SSE-S3, TLS-only policy.
- Lifecycle under `<prefix>/`: Standard-IA after 30 days, delete after 365.
- IAM policy allows only `s3:PutObject` under `<prefix>/`. With `create_user` (default), a user and access key for k3s; on EKS set `create_user = false` and attach `writer_policy_arn` to the operator's IRSA or Pod Identity role.

```sh
terraform init
terraform apply -var bucket_name=my-riffle-archive
kubectl -n riffle-system create secret generic riffle-archive \
  --from-literal=RIFFLE_ARCHIVE_BUCKET="$(terraform output -raw bucket)" \
  --from-literal=AWS_REGION=us-east-1 \
  --from-literal=AWS_ACCESS_KEY_ID="$(terraform output -raw access_key_id)" \
  --from-literal=AWS_SECRET_ACCESS_KEY="$(terraform output -raw secret_access_key)"
```

The secret key is stored in Terraform state; use an encrypted remote backend. Works with `tofu` too.
