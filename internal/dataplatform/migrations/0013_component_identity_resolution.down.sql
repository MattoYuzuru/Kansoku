-- Compatibility rollback removes only the derived view. Additive columns and
-- immutable resolution history remain so a code rollback cannot erase
-- historical evidence.
DROP VIEW IF EXISTS component_assertion_current_resolution;
