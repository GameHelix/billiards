-- A confirmed account can move to another address. The link goes to the new
-- address, which its token keeps until it is used.
alter table email_tokens drop constraint email_tokens_purpose_check;
alter table email_tokens add constraint email_tokens_purpose_check check (purpose in ('verify', 'reset', 'change'));
