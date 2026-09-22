-- name: CreateEnvironment :one
INSERT INTO environments (tenant_id, name, provider, region) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetEnvironment :one
SELECT * FROM environments WHERE id = $1;

-- name: CreateEnvironmentAWSConfig :one
INSERT INTO environment_aws_config (environment_id, account_ref, external_id, trust_anchor)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetEnvironmentAWSConfig :one
SELECT * FROM environment_aws_config WHERE environment_id = $1;

-- name: CreateEnvironmentAWSTierRole :one
INSERT INTO environment_aws_tier_roles (environment_id, tier, role_arn) VALUES ($1, $2, $3) RETURNING *;

-- name: GetEnvironmentAWSTierRole :one
SELECT * FROM environment_aws_tier_roles WHERE environment_id = $1 AND tier = $2;
