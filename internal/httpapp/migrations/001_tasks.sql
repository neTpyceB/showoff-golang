DO $$
BEGIN
  IF to_regclass('public.tasks') IS NULL THEN
    IF to_regtype('public.tasks') IS NOT NULL THEN
      EXECUTE 'DROP TYPE public.tasks';
    END IF;
    EXECUTE '
      CREATE TABLE public.tasks (
        id BIGSERIAL PRIMARY KEY,
        title TEXT NOT NULL,
        note TEXT NOT NULL DEFAULT '''',
        done BOOLEAN NOT NULL DEFAULT FALSE,
        created_at TIMESTAMPTZ NOT NULL,
        updated_at TIMESTAMPTZ NOT NULL
      )';
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_tasks_created_at ON tasks (created_at);
