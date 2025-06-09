-- strips all sensitive data from db
delete from sessions;
update accounts set password = '', username = 'redacted_username_'::text || id::text, email = 'redacted_email_'::text || id::text, account_created = now(), last_seen = now(), email_confirm_code = null, wz_confirm_code = null, wz_recovery_code = null, display_name = null;
delete from chatlog;
delete from eventlog;
delete from reports;

-- trim games table
select now(); -- take the time
delete from players where game = any((select id from games where time_started + '2 months' < '2025-06-08 08:00:00'));
delete from games_rating_categories where game = any((select id from games where time_started + '2 months' < '2025-06-08 08:00:00'));
delete from games where time_started + '2 months' < '2025-06-08 08:00:00';