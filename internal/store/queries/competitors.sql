-- name: CreateManualCompetitor :one
INSERT INTO competitors (id,business_id,name,website,aliases,source,status)
SELECT $1,b.id,$4,$5,$6,'manual','tracked' FROM businesses b
WHERE b.id=$2 AND b.tenant_id=$3
RETURNING id,business_id,name,website,aliases,suggested_aliases,source,status,created_at;

-- name: SetCompetitorStatus :one
UPDATE competitors co SET status=$3 FROM businesses b
WHERE co.id=$1 AND co.business_id=b.id AND b.tenant_id=$2
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;

-- name: ApproveSuggestedAlias :one
UPDATE competitors co
SET suggested_aliases=array_remove(co.suggested_aliases,$3),
    aliases=CASE WHEN $3=ANY(co.aliases) THEN co.aliases ELSE array_append(co.aliases,$3) END
FROM businesses b
WHERE co.id=$1 AND co.business_id=b.id AND b.tenant_id=$2 AND $3=ANY(co.suggested_aliases)
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;

-- name: RejectSuggestedAlias :one
UPDATE competitors co SET suggested_aliases=array_remove(co.suggested_aliases,$3)
FROM businesses b
WHERE co.id=$1 AND co.business_id=b.id AND b.tenant_id=$2 AND $3=ANY(co.suggested_aliases)
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;

-- name: UpdateCompetitorAliases :one
UPDATE competitors co SET aliases=$3 FROM businesses b
WHERE co.id=$1 AND co.business_id=b.id AND b.tenant_id=$2
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;
