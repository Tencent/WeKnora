-- The rewritten rows cannot be told apart from lists users saved with the
-- same tools, so nothing is reverted. The explicit list behaves the same
-- under both meanings of an empty list.
DO $$ BEGIN RAISE NOTICE '[Migration 000116 down] Nothing to revert'; END $$;
