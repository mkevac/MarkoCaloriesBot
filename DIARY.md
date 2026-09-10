# Daily calorie diary

- `/calories` opens preset buttons (1,500, 1,800, 2,000, 2,500 kcal), Custom,
  and Clear target. Custom prompts for a number without requiring a command.
- `/timezone` opens Dubai, Belgrade, London, UTC, and Other city buttons.
  Other city searches the bundled IANA timezone city names, then shows matching
  choices. Try a nearby major city if yours is not listed. The default is UTC.
- `/cancel` exits a custom entry. Menus and prompts expire after 15 minutes or a
  bot restart; open the command again to continue. Saved settings persist.
- Direct commands such as `/calories 2000`, `/calories off`, and
  `/timezone Asia/Dubai` also work. Clearing the target keeps saved meals.
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
