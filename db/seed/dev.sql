-- DEVELOPMENT FIXTURES ONLY. Placeholder catalogue transcribed from the Figma designs (decision D16).
-- Running the seed RESETS all catalogue, cart and order data, and the store settings (back to env defaults).
TRUNCATE categories, products, addons, sessions, orders, payment_events, newsletter_subscribers, store_settings CASCADE;

-- IDs are derived from slugs/SKUs (md5 → uuid) so re-seeding keeps them stable. Otherwise pages cached
-- by the storefront would keep pointing at variant IDs that no longer exist.

INSERT INTO categories (slug, name, eyebrow, description, position) VALUES
('mirrors', 'Architectural Mirrors & Sculptural Forms', 'Handcrafted Silhouettes & Archetypes',
 'Handcrafted pieces engineered with ultra-HD 5mm copper-free silvered glass, heirloom Indian hardwoods, and precision-forged champagne brass frames.', 1),
('table-lamps', 'Table Lamps & Sculptural Lumina', 'Architectural Illumination',
 'Hand-turned solid brass, seasoned hardwood bases, fluted bone porcelain, and mouth-blown opal diffusers. Architectural luminaires calibrated for serene ambient radiance.', 2);

INSERT INTO category_facets (category_id, key, label, kind, position)
SELECT c.id, f.key, f.label, f.kind, f.position FROM categories c
JOIN (VALUES
  ('mirrors', 'shape', 'Category & Shape', 'multi', 1),
  ('mirrors', 'material', 'Frame Material', 'multi', 2),
  ('mirrors', 'room', 'Room Environment', 'multi', 3),
  ('table-lamps', 'form', 'Category & Form', 'multi', 1),
  ('table-lamps', 'finish', 'Material & Finish', 'swatch', 2),
  ('table-lamps', 'warmth', 'Illumination Warmth', 'single', 3)
) AS f(cat, key, label, kind, position) ON f.cat = c.slug;

-- products: slug, category, name, subtitle, material, size, badges, rating, count, shipping, rank
INSERT INTO products (id, slug, category_id, name, subtitle, material_label, size_label, badges, rating_avg, rating_count, shipping_note, rank)
SELECT md5('product:' || p.slug)::uuid, p.slug, c.id, p.name, p.subtitle, p.material, p.size, p.badges::jsonb, p.rating, p.cnt, p.ship, p.rank
FROM (VALUES
  ('biomorphic-duet', 'mirrors', 'The Biomorphic Duet', 'Dual French Cleats • 5mm Ultra-HD Copper-Free', 'Hand-Forged Brass', '',
   '[{"label":"Best Seller","tone":"dark"},{"label":"Console Pair","tone":"light"}]', 4.9, 219, 'Incl. White-Glove Transit • 7-Day In-Home Trial', 1),
  ('pinnacle-arc-supreme', 'mirrors', 'Pinnacle Arc Supreme', 'Ultra-Slim Bevel • Heavyweight Anti-Tip Base', 'Matte Iron • Floor Standing', '',
   '[{"label":"Signature Piece","tone":"gold"},{"label":"6.5ft x 2.8ft","tone":"light"}]', 5.0, 142, 'Ready to Ship • Dispatch in 24h', 2),
  ('driftwood-solstice', 'mirrors', 'Driftwood Solstice', 'Handcarved Live-Edge • Natural Oil Sealer', 'Reclaimed Teak Wood', '',
   '[{"label":"Limited Atelier Run","tone":"dark"},{"label":"4.2ft Wide Statement","tone":"light"}]', 5.0, 58, 'Timber Crate Reinforced Shipping', 3),
  ('contour-architectural-vanity-mirror', 'mirrors', 'Contour Architectural Vanity Mirror', 'Asymmetric Curve • Anti-Fog Thermal Layer', 'Champagne Brass Alloy', '',
   '[{"label":"Moisture-Proof IP54","tone":"dark"},{"label":"36\" x 24\"","tone":"light"}]', 4.9, 112, 'Ideal for Luxury Powder Rooms', 4),
  ('aura-capsule-backlit', 'mirrors', 'Aura Capsule Backlit', 'Touch Dimmer Sensor • Defogger Pad Built-In', 'Aura Halo Series', '',
   '[{"label":"3000K Ambient","tone":"dark"},{"label":"48\" x 20\"","tone":"light"}]', 4.8, 89, 'Pre-wired for Standard 220V Indian Sockets', 5),
  ('palermo-fluted-walnut', 'mirrors', 'Palermo Fluted Walnut', 'Hand-Milled Fluting • Matte Wax Sealed', 'Heritage Joinery', '',
   '[{"label":"Solid Walnut","tone":"dark"},{"label":"54\" x 32\"","tone":"light"}]', 4.9, 64, 'Includes Professional Wall Mount Hardware', 6),
  ('zenith-minimalist-round', 'mirrors', 'Zenith Minimalist Round', 'Seamless Extruded Rim • Anti-Distortion Core', 'Champagne Brass', '',
   '[{"label":"Atelier Classic","tone":"light"},{"label":"Dia 40\"","tone":"light"}]', 4.9, 98, 'Ready to Ship • Dispatch within 24h', 7),
  ('monolith-floor-leaner', 'mirrors', 'Monolith Floor Leaner', 'Reinforced Frame • Floor Anchoring Kit', 'Brushed Champagne Brass', '',
   '[{"label":"Grand Floor Leaner","tone":"dark"},{"label":"7ft x 3.5ft","tone":"light"}]', 5.0, 37, 'White-Glove Assisted Room Placement', 8),
  ('aurelia-arc-desk-lamp', 'table-lamps', 'The Aurelia Arc Desk Lamp', '3000K Warm Ambient • In-Cord Touch Dimmer', 'Hand-Spun Brushed Brass', 'Dia 14" • H 22"',
   '[{"label":"Best Seller","tone":"dark"},{"label":"Atelier Signature","tone":"light"}]', 4.9, 86, 'White-Glove Transit & 3-Yr Luminaire Warranty', 1),
  ('sylva-organic-mushroom-lamp', 'table-lamps', 'Sylva Organic Mushroom Lamp', 'Dual Warm Glowing Orbs • 2700K Sunset Diffuser', 'Sculpted Celadon Clay & Hand-Molded Ceramic', 'H 18"',
   '[{"label":"Limited Atelier Run","tone":"gold"}]', 4.9, 47, 'Zero-Deflection Multi-Tier Timber Crating', 2),
  ('komorebi-walnut-luminaire', 'table-lamps', 'Komorebi Walnut Luminaire', '3-Stage Touch Capacitive Base • 3000K', 'Turned Solid Walnut', 'H 16"',
   '[{"label":"Architectural Guild","tone":"light"}]', 0, 0, 'Jaipur Studio', 3),
  ('lantern-fluted-night-lamp', 'table-lamps', 'Lantern Fluted Night Lamp', 'Integrated LED Halo • Ambient Reading', 'Bone China & Oak Base', 'H 12"',
   '[{"label":"New Arrival","tone":"light"}]', 0, 0, 'Pan-India Ship', 4),
  ('solstice-globe-luminaire', 'table-lamps', 'Solstice Globe Luminaire', 'G9 Warm LED Included • Anti-Glare Frosted Glass', 'Brushed Brass & Frosted Glass', 'H 17"',
   '[{"label":"Dual Mount","tone":"light"}]', 0, 0, 'Ready to Ship', 5)
) AS p(slug, cat, name, subtitle, material, size, badges, rating, cnt, ship, rank)
JOIN categories c ON c.slug = p.cat;

INSERT INTO product_images (product_id, url, alt, position)
SELECT id, '/images/products/' || slug || '.jpg', name, 0 FROM products;

-- Single-variant products: price, MRP, stock.
INSERT INTO product_variants (id, product_id, sku, price_paise, mrp_paise, stock)
SELECT md5('variant:' || v.sku)::uuid, p.id, v.sku, v.price * 100, v.mrp * 100, v.stock
FROM (VALUES
  ('biomorphic-duet', 'BIO-DUET-2', 18499, 22000, 12),
  ('pinnacle-arc-supreme', 'ARC-SUP-6528', 16999, 19999, 8),
  ('driftwood-solstice', 'DRF-SOL-42', 21999, 26000, 3),
  ('aura-capsule-backlit', 'AUR-CAP-4820', 15499, 18500, 10),
  ('palermo-fluted-walnut', 'PAL-FLW-5432', 19499, 24000, 6),
  ('zenith-minimalist-round', 'ZEN-RND-40', 11499, 13500, 15),
  ('monolith-floor-leaner', 'MON-FLR-7035', 27999, 33000, 4),
  ('aurelia-arc-desk-lamp', 'AUR-ARC-22', 14999, 18500, 10),
  ('sylva-organic-mushroom-lamp', 'SYL-MSH-18', 16499, 19000, 7),
  ('komorebi-walnut-luminaire', 'KOM-WAL-16', 18999, 22500, 9),
  ('lantern-fluted-night-lamp', 'LAN-FLT-12', 9999, 12000, 20),
  ('solstice-globe-luminaire', 'SOL-GLB-17', 11499, 13500, 11)
) AS v(slug, sku, price, mrp, stock)
JOIN products p ON p.slug = v.slug;

-- Contour: the fully configurable reference product from the product-page design.
UPDATE products SET
  series = 'Atelier Arc Series',
  description = 'Asymmetrical hand-bent brass silhouette with integrated 3000K warm backlighting and anti-fog heating pad.',
  options = '[
    {"code":"dimension","name":"Select Dimension","kind":"chip","values":[
      {"code":"medium","label":"Medium","detail":"32\" x 22\""},
      {"code":"large","label":"Large","detail":"36\" x 24\""},
      {"code":"grand","label":"Grand","detail":"42\" x 28\""}]},
    {"code":"finish","name":"Hand-Applied Finish","kind":"swatch","values":[
      {"code":"champagne-brass","label":"Brushed Champagne Brass","swatchHex":"#C4A57B"},
      {"code":"aged-gold","label":"Aged Atelier Gold","swatchHex":"#7A5C3A"},
      {"code":"matte-noir","label":"Matte Noir","swatchHex":"#1C1A17"},
      {"code":"patina-bronze","label":"Antique Patina Bronze","swatchHex":"#5A4526"}]},
    {"code":"luminescence","name":"Ambient Luminescence Profile","kind":"chip","values":[
      {"code":"warm-3000k","label":"Warm 3000K Ambient"},
      {"code":"sunset-2700k","label":"Sunset 2700K Glow"}]}
  ]',
  features = '[
    {"title":"Sculptural Fluid Asymmetry","body":"Hand-curved organic perimeter breaks the rigid monotony of rectangular tiling, introducing fluid wabi-sabi elegance to master baths and powder suites."},
    {"title":"Shadowless 3000K Halo","body":"Concealed LED perimeter diffusion projects an even, ambient glow across skin tones without the harsh glare of overhead spotlights."},
    {"title":"Monsoon-Grade Silvering","body":"5mm Saint-Gobain float glass with copper-free backing and triple-sealed edges, preventing oxidation and black-edge clouding in humid coastal climates."},
    {"title":"Integrated Thermal Demister","body":"Hidden heating pad keeps the central sightline pristine and fog-free throughout long, high-steam showers."}
  ]'
WHERE slug = 'contour-architectural-vanity-mirror';

INSERT INTO product_variants (id, product_id, sku, options, price_paise, mrp_paise, stock, position)
SELECT md5('variant:ARC-CNT-' || d.sku || '-' || f.sku || '-' || l.sku)::uuid, p.id,
       'ARC-CNT-' || d.sku || '-' || f.sku || '-' || l.sku,
       jsonb_build_object('dimension', d.code, 'finish', f.code, 'luminescence', l.code),
       d.price * 100, d.mrp * 100, 5,
       d.pos * 100 + f.pos * 10 + l.pos
FROM products p
CROSS JOIN (VALUES ('large', '3624', 12999, 15999, 1), ('medium', '3222', 10999, 13499, 2), ('grand', '4228', 16499, 19999, 3)) AS d(code, sku, price, mrp, pos)
CROSS JOIN (VALUES ('champagne-brass', 'BR', 1), ('aged-gold', 'AG', 2), ('matte-noir', 'MN', 3), ('patina-bronze', 'PB', 4)) AS f(code, sku, pos)
CROSS JOIN (VALUES ('warm-3000k', '30', 1), ('sunset-2700k', '27', 2)) AS l(code, sku, pos)
WHERE p.slug = 'contour-architectural-vanity-mirror';

INSERT INTO product_images (product_id, url, alt, position)
SELECT p.id, '/images/products/contour-architectural-vanity-mirror-' || i.n || '.jpg', p.name || ' — ' || i.alt, i.n
FROM products p
CROSS JOIN (VALUES (2, 'halo LED'), (3, 'brass bezel detail'), (4, 'French cleat mount'), (5, 'dimension drawing')) AS i(n, alt)
WHERE p.slug = 'contour-architectural-vanity-mirror';

INSERT INTO product_attributes (product_id, key, value, label, swatch_hex)
SELECT p.id, a.key, a.value, a.label, a.hex
FROM (VALUES
  ('biomorphic-duet', 'shape', 'statement-duets', 'Wall Statement Duets', ''),
  ('biomorphic-duet', 'material', 'hand-forged-brass', 'Hand-Forged Brass', ''),
  ('biomorphic-duet', 'room', 'living-room', 'Living Room', ''),
  ('biomorphic-duet', 'room', 'grand-foyer', 'Grand Foyer', ''),
  ('pinnacle-arc-supreme', 'shape', 'arch-crown', 'Arch & Crown Curved', ''),
  ('pinnacle-arc-supreme', 'material', 'anodized-matte-iron', 'Anodized Matte Iron', ''),
  ('pinnacle-arc-supreme', 'room', 'dressing-suite', 'Dressing Suite', ''),
  ('driftwood-solstice', 'shape', 'organic-freeform', 'Organic & Freeform', ''),
  ('driftwood-solstice', 'material', 'solid-reclaimed-teak', 'Solid Reclaimed Teak', ''),
  ('driftwood-solstice', 'room', 'powder-room', 'Powder Room', ''),
  ('driftwood-solstice', 'room', 'master-bed', 'Master Bed', ''),
  ('contour-architectural-vanity-mirror', 'shape', 'organic-freeform', 'Organic & Freeform', ''),
  ('contour-architectural-vanity-mirror', 'material', 'hand-forged-brass', 'Hand-Forged Brass', ''),
  ('contour-architectural-vanity-mirror', 'room', 'powder-room', 'Powder Room', ''),
  ('aura-capsule-backlit', 'shape', 'pill-capsule', 'Pill & Capsule Statement', ''),
  ('aura-capsule-backlit', 'material', 'hand-forged-brass', 'Hand-Forged Brass', ''),
  ('aura-capsule-backlit', 'room', 'powder-room', 'Powder Room', ''),
  ('palermo-fluted-walnut', 'shape', 'arch-crown', 'Arch & Crown Curved', ''),
  ('palermo-fluted-walnut', 'material', 'fluted-walnut-hardwood', 'Fluted Walnut Hardwood', ''),
  ('palermo-fluted-walnut', 'room', 'grand-foyer', 'Grand Foyer', ''),
  ('zenith-minimalist-round', 'shape', 'circle-lunar-oval', 'Circle & Lunar Oval', ''),
  ('zenith-minimalist-round', 'material', 'hand-forged-brass', 'Hand-Forged Brass', ''),
  ('zenith-minimalist-round', 'room', 'living-room', 'Living Room', ''),
  ('monolith-floor-leaner', 'material', 'hand-forged-brass', 'Hand-Forged Brass', ''),
  ('monolith-floor-leaner', 'room', 'dressing-suite', 'Dressing Suite', ''),
  ('aurelia-arc-desk-lamp', 'form', 'minimal-arch', 'Minimal Arch', ''),
  ('aurelia-arc-desk-lamp', 'finish', 'brushed-champagne-brass', 'Brushed Champagne Brass', '#C4A57B'),
  ('aurelia-arc-desk-lamp', 'warmth', '3000k', '3000K Warm Ambient', ''),
  ('sylva-organic-mushroom-lamp', 'form', 'dome-mushroom', 'Dome & Mushroom', ''),
  ('sylva-organic-mushroom-lamp', 'finish', 'alabaster-opal', 'Alabaster & Opal', '#E8E4DC'),
  ('sylva-organic-mushroom-lamp', 'warmth', '2700k', '2700K Sunset Glow', ''),
  ('komorebi-walnut-luminaire', 'form', 'dome-mushroom', 'Dome & Mushroom', ''),
  ('komorebi-walnut-luminaire', 'finish', 'seasoned-walnut', 'Seasoned Walnut', '#5B3A29'),
  ('komorebi-walnut-luminaire', 'warmth', '3000k', '3000K Warm Ambient', ''),
  ('lantern-fluted-night-lamp', 'form', 'fluted-ceramic', 'Fluted Ceramic', ''),
  ('lantern-fluted-night-lamp', 'finish', 'alabaster-opal', 'Alabaster & Opal', '#E8E4DC'),
  ('lantern-fluted-night-lamp', 'warmth', 'dimmable', 'Dimmable Integrated LED', ''),
  ('solstice-globe-luminaire', 'form', 'articulated-stem', 'Articulated Stem', ''),
  ('solstice-globe-luminaire', 'finish', 'brushed-champagne-brass', 'Brushed Champagne Brass', '#C4A57B'),
  ('solstice-globe-luminaire', 'warmth', '3000k', '3000K Warm Ambient', '')
) AS a(slug, key, value, label, hex)
JOIN products p ON p.slug = a.slug;

-- Product-page content for Contour (Figma "Engineered with Sculptural Precision" + care section).
-- The other three spec accordion sections are collapsed in Figma, so their copy is missing (ROADMAP B6).
UPDATE products SET
  specs = '[
    {"title":"Materials & Glass Provenance",
     "body":"We use pure copper-free, lead-free 5mm Saint-Gobain float glass engineered with a multi-layered silver backing that resists black-edge oxidation even in coastal cities like Mumbai, Chennai, and Kochi.",
     "bullets":["Aircraft-grade aluminum structural chassis (100% rustproof in continuous steam).","Solid brass outer rim, shaped by three successive hand-rolling mandrels.","Shatter-proof safety vinyl membrane laminated across the entire back panel."]}
  ]',
  facts = '[{"label":"Total Depth","value":"1.8 inches (45mm)"},{"label":"Net Weight","value":"8.4 kg (18.5 lbs)"}]',
  care = '[
    {"eyebrow":"Reflective Clarity","title":"Glass & Mirror Care","body":"Mist pure water or non-ammonia cleanser onto a microfiber cloth first; never spray fluid directly onto the glass perimeter or bezel seam."},
    {"eyebrow":"Patina Longevity","title":"Champagne Brass Rim","body":"Dust weekly with a soft dry flannel. Brass naturally matures into a subtle warm patina; apply non-abrasive natural wax once annually to maintain luster."},
    {"eyebrow":"Thermal Efficiency","title":"Anti-Fog Film & Energy Protocol","body":"Activate 2 minutes before bathing. The integrated 25W PTC heating element operates with auto-standby and consumes negligible power, reaching optimal optical clarity rapidly."},
    {"eyebrow":"Structural Integrity","title":"Re-Sealing & Cleat Inspection","body":"Inspect French cleat wall anchors and verify spirit leveling annually. Inspect perimeter silicone seals to safeguard against mineral hard-water deposition in coastal climates."},
    {"eyebrow":"Circuit Safety","title":"Electrical & Demister Core","body":"Powered by 220–240V moisture-shielded transformer. Ensure the demister toggle is switched off when not in active use."},
    {"eyebrow":"Climate Resilience","title":"Monsoon Moisture Management","body":"IP54 sealed against splashing and steam. In coastal cities, ensure cross-ventilation after steam baths to maintain longevity."}
  ]'
WHERE slug = 'contour-architectural-vanity-mirror';

-- Written reviews shown in the Figma product page.
INSERT INTO product_reviews (product_id, rating, author_name, author_meta, body, variant_label, verified, status, created_at)
SELECT p.id, r.rating, r.author, r.meta, r.body, r.variant, true, 'published', r.created::timestamptz
FROM products p
JOIN (VALUES
  ('contour-architectural-vanity-mirror', 5, 'Rohit K.', 'Principal Architect, Delhi NCR',
   'The brass curvature is exceptionally finished with no visible joints or machine marks. The 3000K backlight has zero hotspotting—the diffusion channel makes it feel like an architectural light cove rather than an appliance. Delivered to Delhi in a heavy wooden crate with zero issues.',
   'Brushed Champagne Brass (36" x 24")', '2025-07-14'),
  ('contour-architectural-vanity-mirror', 5, 'Suhasini Mehta', 'Homeowner, Juhu, Mumbai',
   'Living right across the Arabian Sea, our old mirrors suffered from edge corrosion within months. The Pinnacle Contour has been in our guest powder room for over six monsoon weeks now and remains pristine. The anti-fog heating element works like magic.',
   'Brushed Champagne Brass (32" x 22")', '2025-08-02')
) AS r(slug, rating, author, meta, body, variant, created) ON r.slug = p.slug;

-- Rating-only fixtures so each product's aggregate matches its Figma count/average:
-- round(count × (avg − 4)) five-star ratings, the rest four-star.
INSERT INTO product_reviews (product_id, rating, verified, status, created_at)
SELECT p.id, CASE WHEN g <= round(p.rating_count * (p.rating_avg - 4)) THEN 5 ELSE 4 END, true, 'published',
       timestamptz '2025-09-01' - g * interval '1 day'
FROM products p
CROSS JOIN LATERAL generate_series(1, p.rating_count - (SELECT count(*) FROM product_reviews r WHERE r.product_id = p.id)) AS g
WHERE p.rating_count > 0;

-- products.rating_avg / rating_count are derived from published reviews (single source of truth).
UPDATE products p SET rating_avg = a.avg, rating_count = a.cnt
FROM (SELECT product_id, round(avg(rating)::numeric, 1) AS avg, count(*)::int AS cnt
      FROM product_reviews WHERE status = 'published' GROUP BY product_id) a
WHERE a.product_id = p.id;

-- "Pairs well with": each mirror → top three lamps, each lamp → top three mirrors (by featured rank).
INSERT INTO product_relations (product_id, related_id, position)
SELECT p.id, o.id, o.pos
FROM products p
JOIN categories c ON c.id = p.category_id
CROSS JOIN LATERAL (
  SELECT q.id, row_number() OVER (ORDER BY q.rank)::int AS pos
  FROM products q JOIN categories qc ON qc.id = q.category_id
  WHERE qc.slug <> c.slug
  ORDER BY q.rank LIMIT 3
) o;

-- Add-on prices are UNCONFIRMED (ROADMAP B4): checkout shows ₹499 for wall mount, cart page ₹999.
INSERT INTO addons (code, name, description, price_paise, position) VALUES
('white-glove-mount', 'White-Glove Wall Mount', 'Laser-leveled hanging, rawlplug anchoring by Pinnacle verified technicians.', 49900, 1),
('care-kit', 'Anti-Static Care Kit', 'Belgian microfiber + formula', 69900, 2);
