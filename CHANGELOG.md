# Changelog

## v0.2.1 - 2026-10-12

- Fixed sender routing to newly created folders by refreshing mailbox metadata before learning moves and preserving failed refreshes for retry.
- Refresh folder names, hierarchy, and roles on each monitoring cycle so renamed, moved, or deleted folders update the generated Sieve rules even when no email changes occur.
- Preserve configured Screener and Junk folder identities across renames and restarts, with a fallback to the configured name when a saved folder is deleted and recreated.
- Fixed an endless Junk rescue loop for senders intentionally routed to Junk.
- Preserve genuine user moves after Junk rescue, including moving a rescued message back to Screener to reset its sender route.
- Protect Junk rescue against concurrent user moves by checking current folder membership and making each move conditional on the latest email state.

## v0.2.0 - 2026-07-24

- Added an optional JMAP advertised-host override for split-host and proxy migrations, allowing discovered Sieve upload and WebSocket endpoints to use a trusted hostname while preserving session discovery.

## v0.1.0 - 2026-07-05

- Initial release of JMAP Screener with sender-to-mailbox learning, Sieve script generation, JMAP polling/push support, junk rescue, configuration, Docker support, and health checks.
