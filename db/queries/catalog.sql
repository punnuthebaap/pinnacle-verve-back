-- name: ListCategories :many
SELECT c.slug, c.name, c.eyebrow, c.description,
       (SELECT count(*) FROM products p WHERE p.category_id = c.id AND p.is_active)::int AS product_count
FROM categories c
WHERE c.is_active
ORDER BY c.position, c.name;

-- name: GetCategoryBySlug :one
SELECT c.id, c.slug, c.name, c.eyebrow, c.description,
       (SELECT count(*) FROM products p WHERE p.category_id = c.id AND p.is_active)::int AS product_count
FROM categories c
WHERE c.slug = $1 AND c.is_active;

-- name: ListCategoryFacetValues :many
SELECT f.key, f.label, f.kind, pa.value, pa.label AS value_label, pa.swatch_hex,
       count(*)::int AS product_count
FROM category_facets f
JOIN products p ON p.category_id = f.category_id AND p.is_active
JOIN product_attributes pa ON pa.product_id = p.id AND pa.key = f.key
WHERE f.category_id = $1
GROUP BY f.key, f.label, f.kind, f.position, pa.value, pa.label, pa.swatch_hex
ORDER BY f.position, pa.label;

-- The display variant is the first active variant by position; its price is the listed price.
-- Facet filters: `filters` holds "key:value" pairs and `filter_keys` their distinct keys; for each key
-- the product must match at least one requested value (OR within a facet, AND across facets).
-- name: ListProducts :many
SELECT p.id, p.slug, p.name, p.subtitle, p.material_label, p.size_label, p.badges,
       p.rating_avg, p.rating_count, p.shipping_note,
       dv.id AS variant_id, dv.price_paise, dv.mrp_paise,
       st.total_stock,
       coalesce(img.url, '')::text AS image_url, coalesce(img.alt, '')::text AS image_alt,
       count(*) OVER ()::int AS total_count
FROM products p
JOIN categories c ON c.id = p.category_id AND c.is_active
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
WHERE p.is_active
  AND (sqlc.narg(category)::text IS NULL OR c.slug = sqlc.narg(category))
  AND (sqlc.narg(min_price)::bigint IS NULL OR dv.price_paise >= sqlc.narg(min_price))
  AND (sqlc.narg(max_price)::bigint IS NULL OR dv.price_paise <= sqlc.narg(max_price))
  AND (NOT sqlc.arg(in_stock)::bool OR st.total_stock > 0)
  AND NOT EXISTS (
      SELECT 1 FROM unnest(sqlc.arg(filter_keys)::text[]) AS fk(key)
      WHERE NOT EXISTS (
          SELECT 1 FROM product_attributes pa
          WHERE pa.product_id = p.id AND pa.key = fk.key
            AND (pa.key || ':' || pa.value) = ANY(sqlc.arg(filters)::text[])
      )
  )
ORDER BY
  CASE WHEN sqlc.arg(sort)::text = 'price_asc'  THEN dv.price_paise END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'price_desc' THEN dv.price_paise END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'rating'     THEN p.rating_avg END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'newest'     THEN p.created_at END DESC,
  p.rank, p.name
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: GetProductBySlug :one
SELECT p.*, c.slug AS category_slug, c.name AS category_name
FROM products p
JOIN categories c ON c.id = p.category_id
WHERE p.slug = $1 AND p.is_active;

-- name: ListProductImages :many
SELECT url, alt FROM product_images WHERE product_id = $1 ORDER BY position;

-- name: ListProductVariants :many
SELECT id, sku, options, price_paise, mrp_paise, stock
FROM product_variants
WHERE product_id = $1 AND is_active
ORDER BY position, sku;
