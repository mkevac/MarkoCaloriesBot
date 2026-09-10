# Clarifying a meal

Send a photo or album, then reply with text to either an original photo or the
bot's answer. For example: “There is no potato; that is cabbage.” The bot sends
all original photos to the LLM again with their captions and your clarifications,
and replies to your clarification with revised food and macro estimates.

Further replies to the original photos, any bot answer, or a previous clarification
keep the earlier clarifications. The latest clarification takes precedence when
information conflicts. Only the original sender can clarify that meal, in the
same chat. A new photo message starts a new meal; standalone text is ignored.

History is persisted in the same SQLite database as usage stats (`STATS_DB_PATH`).
It stores Telegram file IDs rather than expiring download URLs and fetches fresh
URLs for every analysis. Keep the existing database/volume across restarts.

Bot answers sent before this feature was installed have no saved photo link;
resend the photo or album to begin a correction thread. A direct reply to an old
single photo can recover it from Telegram's embedded reply data. Unknown old
albums must be resent in full. The bot explains when it cannot find the context.

Each clarification is counted as a request in `/stats`.
