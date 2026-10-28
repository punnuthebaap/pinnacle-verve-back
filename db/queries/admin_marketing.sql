-- name: AdminListReviews :many
SELECT r.id, r.rating, r.author_name, r.author_meta, r.body, r.variant_label, r.verified, r.status, r.created_at,
       r.moderated_at, coalesce(u.name, '')::text AS moderated_by_name,
       p.id AS product_id, p.slug AS product_slug, p.name AS product_name,
       count(*) OVER ()::int AS total_count
FROM product_reviews r
JOIN products p ON p.id = r.product_id
LEFT JOIN admin_users u ON u.id = r.moderated_by
WHERE (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status))
  AND (sqlc.narg(product_id)::uuid IS NULL OR r.product_id = sqlc.narg(product_id))
  AND (sqlc.narg(rating)::smallint IS NULL OR r.rating = sqlc.narg(rating))
  AND (NOT sqlc.arg(written_only)::bool OR r.body <> '')
  AND (sqlc.arg(q)::text = '' OR r.body ILIKE '%' || sqlc.arg(q) || '%' OR r.author_name ILIKE '%' || sqlc.arg(q) || '%'
       OR p.name ILIKE '%' || sqlc.arg(q) || '%')
ORDER BY r.created_at DESC, r.id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: AdminCountPendingReviews :one
SELECT count(*)::int FROM product_reviews WHERE status = 'pending';

-- name: AdminSetReviewStatus :one
UPDATE product_reviews SET status = $2, moderated_at = now(), moderated_by = $3
WHERE id = $1
RETURNING product_id, status;

-- name: AdminGetReviewStatus :one
SELECT status FROM product_reviews WHERE id = $1 FOR UPDATE;

-- products.rating_avg / rating_count are derived from published reviews (single source of truth).
-- name: RecomputeProductRating :exec
UPDATE products p
SET rating_avg = coalesce((SELECT round(avg(rating)::numeric, 1) FROM product_reviews WHERE product_id = p.id AND status = 'published'), 0),
    rating_count = (SELECT count(*) FROM product_reviews WHERE product_id = p.id AND status = 'published')
WHERE p.id = $1;

-- name: AdminListSubscribers :many
SELECT email, created_at, count(*) OVER ()::int AS total_count
FROM newsletter_subscribers
WHERE sqlc.arg(q)::text = '' OR email ILIKE '%' || sqlc.arg(q) || '%'
ORDER BY created_at DESC, email
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: AdminAllSubscribers :many
SELECT email, created_at FROM newsletter_subscribers ORDER BY created_at, email;

-- name: AdminDeleteSubscriber :execrows
DELETE FROM newsletter_subscribers WHERE email = lower(sqlc.arg(email)::text);
