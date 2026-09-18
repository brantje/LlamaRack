-- +goose Up
ALTER TABLE instances ADD COLUMN slug TEXT;
ALTER TABLE models ADD COLUMN slug TEXT;
ALTER TABLE inference_requests ADD COLUMN model_slug TEXT NOT NULL DEFAULT '';

UPDATE instances SET slug=id WHERE slug IS NULL OR btrim(slug)='';
UPDATE models SET slug=id WHERE slug IS NULL OR btrim(slug)='';
ALTER TABLE instances ALTER COLUMN slug SET NOT NULL;
ALTER TABLE models ALTER COLUMN slug SET NOT NULL;

CREATE UNIQUE INDEX instances_slug_uidx ON instances(slug);
CREATE UNIQUE INDEX models_slug_uidx ON models(slug);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION instances_slug_default_insert_fn()
RETURNS trigger AS $$
BEGIN
 IF NEW.slug IS NULL OR btrim(NEW.slug)='' THEN NEW.slug := NEW.id; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION instances_slug_required_update_fn()
RETURNS trigger AS $$
BEGIN
 IF NEW.slug IS NULL OR btrim(NEW.slug)='' THEN RAISE EXCEPTION 'instance slug is required'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION models_slug_default_insert_fn()
RETURNS trigger AS $$
BEGIN
 IF NEW.slug IS NULL OR btrim(NEW.slug)='' THEN NEW.slug := NEW.id; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION models_slug_required_update_fn()
RETURNS trigger AS $$
BEGIN
 IF NEW.slug IS NULL OR btrim(NEW.slug)='' THEN RAISE EXCEPTION 'model slug is required'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER instances_slug_default_insert BEFORE INSERT ON instances
FOR EACH ROW EXECUTE FUNCTION instances_slug_default_insert_fn();
CREATE TRIGGER instances_slug_required_update BEFORE UPDATE OF slug ON instances
FOR EACH ROW EXECUTE FUNCTION instances_slug_required_update_fn();
CREATE TRIGGER models_slug_default_insert BEFORE INSERT ON models
FOR EACH ROW EXECUTE FUNCTION models_slug_default_insert_fn();
CREATE TRIGGER models_slug_required_update BEFORE UPDATE OF slug ON models
FOR EACH ROW EXECUTE FUNCTION models_slug_required_update_fn();

-- PostgreSQL was first supported after the SQLite resource-identity migration,
-- so there is no supported PostgreSQL v1 data requiring UUID remapping.

-- +goose Down
DROP TRIGGER IF EXISTS models_slug_required_update ON models;
DROP TRIGGER IF EXISTS models_slug_default_insert ON models;
DROP TRIGGER IF EXISTS instances_slug_required_update ON instances;
DROP TRIGGER IF EXISTS instances_slug_default_insert ON instances;
DROP FUNCTION IF EXISTS models_slug_required_update_fn();
DROP FUNCTION IF EXISTS models_slug_default_insert_fn();
DROP FUNCTION IF EXISTS instances_slug_required_update_fn();
DROP FUNCTION IF EXISTS instances_slug_default_insert_fn();
DROP INDEX IF EXISTS models_slug_uidx;
DROP INDEX IF EXISTS instances_slug_uidx;
ALTER TABLE inference_requests DROP COLUMN model_slug;
ALTER TABLE models DROP COLUMN slug;
ALTER TABLE instances DROP COLUMN slug;
