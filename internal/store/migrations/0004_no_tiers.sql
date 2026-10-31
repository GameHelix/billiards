-- Tiers are gone: the radar is free and trades pay a fee each, so an
-- account has no plan.
alter table users drop column tier;
