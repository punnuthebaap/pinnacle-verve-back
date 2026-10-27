-- name: GetActiveProductIDBySlug :one
SELECT id FROM products WHERE slug = $1 AND is_active;

-- name: ReviewDistribution :many
SELECT rating, count(*)::int AS reviews
FROM product_reviews
WHERE product_id = $1 AND status = 'published'
GROUP BY rating;

-- Written reviews only (rating-only entries count towards the summary but are not listed).
-- name: ListWrittenReviews :many
SELECT id, rating, author_name, author_meta, body, variant_label, verified, created_at,
       count(*) OVER ()::int AS total_count
FROM product_reviews
WHERE product_id = $1 AND status = 'published' AND body <> ''
ORDER BY created_at DESC, id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- Same columns as ListProducts (incl. total_count) so the row types are convertible and share one mapper.
-- name: ListRelatedProducts :many
SELECT p.id, p.slug, p.name, p.subtitle, p.material_label, p.size_label, p.badges,
       p.rating_avg, p.rating_count, p.shipping_note,
       dv.id AS variant_id, dv.price_paise, dv.mrp_paise,
       st.total_stock,
       coalesce(img.url, '')::text AS image_url, coalesce(img.alt, '')::text AS image_alt,
       count(*) OVER ()::int AS total_count
FROM product_relations r
JOIN products p ON p.id = r.related_id AND p.is_active
JOIN LATERAL (
    SELECT v.id, v.price_paise, v.mrp_paise FROM product_variants v
    WHERE v.product_id = p.id AND v.is_active ORDER BY v.position, v.sku LIMIT 1
) dv ON true
JOIN LATERAL (
    SELECT coalesce(sum(v.stock), 0)::int AS total_stock FROM product_variants v
    WHERE v.product_id = p.id AND v.is_active
) st ON true
LEFT JOIN LATERAL (
    SELECT i.url, i.alt FROM product_images i WHERE i.product_id = p.id ORDER BY i.position LIMIT 1
) img ON true
WHERE r.product_id = $1
ORDER BY r.position, p.name
LIMIT 12;
