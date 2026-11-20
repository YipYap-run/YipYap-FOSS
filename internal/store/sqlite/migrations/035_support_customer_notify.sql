-- Customer notification defaults for support tickets.
INSERT OR IGNORE INTO support_settings (key, value) VALUES
    ('notify_customer_on_reply', 'true'),
    ('notify_customer_on_close', 'true');
