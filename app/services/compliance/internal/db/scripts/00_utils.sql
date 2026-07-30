-- Shared trigger function: keeps updated_at fresh on every UPDATE.
-- Runs before migrations so table triggers can reference it.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
