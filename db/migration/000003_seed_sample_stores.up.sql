-- DEV SAMPLE DATA: a few approved stores + menu items so the user-facing read
-- endpoints return data before the admin portal exists. Safe to drop — run
-- `migrate down` past this, or delete this pair, before real production seeding.

INSERT INTO stores (id, ward_id, name, address, latitude, longitude, is_gf_oriented, opening_hours, status, approved_at)
VALUES
    ('11111111-1111-1111-1111-111111111111',
     (SELECT id FROM wards WHERE name_en = 'Shibuya'),
     'Gluten-Free Bistro Shibuya', '1-2-3 Dogenzaka, Shibuya-ku, Tokyo',
     35.658034, 139.701636, true,
     '[{"day":1,"open":"1100","close":"2200"},{"day":2,"open":"1100","close":"2200"},{"day":3,"open":"1100","close":"2200"}]'::jsonb,
     'approved', now()),
    ('22222222-2222-2222-2222-222222222222',
     (SELECT id FROM wards WHERE name_en = 'Shinjuku'),
     'Rice Flour Ramen Shinjuku', '4-5-6 Kabukicho, Shinjuku-ku, Tokyo',
     35.694003, 139.703540, false,
     '[{"day":0,"open":"1700","close":"2300"},{"day":6,"open":"1700","close":"2300"}]'::jsonb,
     'approved', now());

INSERT INTO menu_items (store_id, name, price_yen, image_url, gf_status, gf_note, sort_order, is_available)
VALUES
    ('11111111-1111-1111-1111-111111111111', 'GF Margherita Pizza',  1800, NULL, 'certified',
     'Made with certified GF flour baked in a dedicated oven.', 1, true),
    ('11111111-1111-1111-1111-111111111111', 'Quinoa Salad Bowl',    1200, NULL, 'certified',
     NULL, 2, true),
    ('22222222-2222-2222-2222-222222222222', 'Rice Flour Shoyu Ramen', 1300, NULL, 'on_request',
     'Ask staff for the GF noodle swap; broth is GF by default.', 1, true),
    ('22222222-2222-2222-2222-222222222222', 'Gyoza (contains wheat)',  600, NULL, 'contains_hidden_gluten',
     'Wrappers contain wheat — listed for transparency.', 2, false);
