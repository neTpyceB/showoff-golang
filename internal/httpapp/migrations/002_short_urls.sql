DO $$
BEGIN
  IF to_regclass('public.short_urls') IS NULL THEN
    IF to_regtype('public.short_urls') IS NOT NULL THEN
      EXECUTE 'DROP TYPE public.short_urls';
    END IF;
    EXECUTE '
      CREATE TABLE IF NOT EXISTS public.short_urls (
        id BIGSERIAL PRIMARY KEY,
        code VARCHAR(32) NOT NULL UNIQUE,
        target_url TEXT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL
      )';
  END IF;
END $$;
