# Marko Calories Bot

A Telegram bot that estimates calories, protein, fat, and carbohydrates from food
photos using the OpenAI Responses API. Send a photo or album, clarify the estimate
by replying, and save meals to track your daily calorie total.

## Installation

1. Create a Telegram bot with [BotFather](https://t.me/BotFather) and keep its token.
2. Create an OpenAI API key with access to the model configured in `openai.go`.
3. Clone this repository:

   ```sh
   git clone https://github.com/mkevac/MarkoCaloriesBot.git
   cd MarkoCaloriesBot
   ```

4. Create a `.env` file:

   ```dotenv
   TELEGRAM_BOT_API_TOKEN=<telegram_bot_token>
   OPENAI_API_KEY=<openai_api_key>
   ADMIN_USERNAME=<your_telegram_username>
   BOT_VERSION=1.0.0
   ```

5. Pull the published image and start the bot:

   ```sh
   docker compose pull markocaloriesbot
   docker compose up -d markocaloriesbot
   ```

6. Send a food photo to your bot on Telegram.

`BOT_VERSION` selects a published Docker version. Omit it to follow `latest`.

## Usage

### User Commands

- **Photo analysis:** Send a food photo or album. Captions can describe ingredients,
  quantities, or preparation. The bot replies with individual foods and total
  calories and macronutrients.
- **Clarifications:** Reply to your photo, the bot's answer, or an earlier
  clarification with text such as “That is cabbage, not potato.” The bot analyzes
  the original photos again, retaining earlier clarifications. Only the original
  sender can clarify a meal in the same chat.
- **Save a meal:** Reply `save` to your photo or the bot's estimate. Only saved
  meals count toward your daily total. Saving the same meal again does not count
  it twice. After a clarification, save the revised answer to update the diary.
- `/calories`: Choose a daily calorie target using buttons or a custom entry.
  `/calories 2000` and `/calories off` also work.
- `/timezone`: Choose your city or timezone. `/timezone Asia/Dubai` also works.
  The default timezone is UTC.
- `/today`: Show today's saved calories, meal count, and remaining calorie target.
- `/cancel`: Cancel a pending settings entry.

See [CORRECTIONS.md](CORRECTIONS.md) and [DIARY.md](DIARY.md) for details.

### Admin Commands

The Telegram account named by `ADMIN_USERNAME` can use `/stats` to see user and
request counts, recent usage, averages, and top users. Failed analyses count as
requests; an album counts once. See [STATS.md](STATS.md).

## Development and Testing

Use Go 1.24 or later and [just](https://github.com/casey/just):

```sh
just test
just build
```

To run directly without Docker:

```sh
go run .
```

The bot loads `.env` from the working directory. The model is configured in
[openai.go](openai.go).

## Versioning

Git tags use `vMAJOR.MINOR.PATCH`; Docker images use the version without `v`,
for example `mkevac/markocaloriesbot:1.0.0`.

- `just version`: Show the current version.
- `just bump`: Tag the next minor version on a clean, committed checkout.
- `just push`: Publish `linux/amd64` and `linux/arm64` images with version and
  `latest` tags. Requires a version tag on the current commit and a clean checkout.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
