-- name: CreateManualCompetitor :one
INSERT INTO competitors (id,business_id,name,website,aliases,source,status)
SELECT @id,b.id,@name,@website,@aliases,'manual','tracked' FROM businesses b
WHERE b.id = @business_id AND b.account_id = @account_id
RETURNING id,business_id,name,website,aliases,suggested_aliases,source,status,created_at;

-- name: SetCompetitorStatus :one
UPDATE competitors co SET status = @status FROM businesses b
WHERE co.id = @id AND co.business_id=b.id AND b.account_id = @account_id
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;

-- name: ApproveSuggestedAlias :one
UPDATE competitors co
SET suggested_aliases=array_remove(co.suggested_aliases,@array_remove),
    aliases=CASE WHEN @array_remove=ANY(co.aliases) THEN co.aliases ELSE array_append(co.aliases,@array_remove) END
FROM businesses b
WHERE co.id = @id AND co.business_id=b.id AND b.account_id = @account_id AND @array_remove=ANY(co.suggested_aliases)
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;

-- name: RejectSuggestedAlias :one
UPDATE competitors co SET suggested_aliases=array_remove(co.suggested_aliases,@array_remove)
FROM businesses b
WHERE co.id = @id AND co.business_id=b.id AND b.account_id = @account_id AND @array_remove=ANY(co.suggested_aliases)
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;

-- name: UpdateCompetitorAliases :one
UPDATE competitors co SET aliases = @aliases FROM businesses b
WHERE co.id = @id AND co.business_id=b.id AND b.account_id = @account_id
RETURNING co.id,co.business_id,co.name,co.website,co.aliases,co.suggested_aliases,co.source,co.status,co.created_at;

-- name: LoadCompetitorForClaim :one
SELECT co.id,co.business_id,co.name,co.aliases,b.aliases::text[] AS business_aliases
FROM competitors co JOIN businesses b ON b.id = co.business_id
WHERE co.id = @id AND b.account_id = @account_id
-- Locks the business too, not just the competitor: the claim reads b.aliases,
-- merges in Go, and writes the whole array back, so two concurrent claims on
-- different competitors of one business would otherwise lose the first merge.
FOR UPDATE OF co, b;

-- name: ReassignCompetitorMentionsToSelf :exec
UPDATE mentions SET subject='self',competitor_id=NULL WHERE competitor_id = @competitor_id;

-- name: DeleteCompetitor :exec
DELETE FROM competitors WHERE id = @id;

-- name: SetBusinessAliases :exec
UPDATE businesses SET aliases = @aliases::text[] WHERE id = @business_id;
