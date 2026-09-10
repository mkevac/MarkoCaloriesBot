# Daily calorie diary

- `/calories 2000` sets your recurring daily target. `/calories` shows progress;
  `/calories off` removes the target without deleting saved meals.
- `/timezone Asia/Dubai` sets the timezone used for new saves. The default is UTC.
- `/today` shows the day's logged calorie total, number of meals, and, when a
  target exists, calories remaining or calories over target.

After a meal is analyzed, reply `save` to your original photo (any photo in an
album) or the bot's estimate. Saving does not call the LLM. Only saved meals count
toward your total. Your totals and target are shared across chats by Telegram
user ID, and only you can save your meals.

A reply to an estimate saves exactly that version. A reply to a photo saves its
latest completed estimate. If a clarification is still awaiting analysis, wait
for its answer. Older analyses from before this feature was installed need a new
analysis before they can be saved.

The first save assigns the meal to the local date when you sent `save`. Saving
it again does not add another meal or move it to another day. To replace a saved
estimate after a correction, reply `save` to the revised answer; the original
saved date stays the same. Timezone changes affect new saves; they do not move
previously saved entries between days.

Settings, estimates, and saved meals persist in the existing SQLite database
configured by `STATS_DB_PATH`. Meal entries include the structured food and macro
estimate. Back up that database/volume to preserve both the diary and usage stats.
