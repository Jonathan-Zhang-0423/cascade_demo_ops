const dailyAgentHeadlines = [
  "Let’s make the product explain itself.",
  "Turn the happy path into a good story.",
  "Your product has a point. Let’s make it visible.",
  "A useful demo starts with one honest sentence.",
  "Let’s give your best workflow a proper entrance.",
  "Less screen tour, more point made.",
  "Find the shortest path to the “aha.”",
  "Make the clicks earn their screen time.",
  "Today’s agenda: one clear story, zero wandering.",
  "Let’s turn product behavior into customer proof.",
  "The product is ready. Let’s make the story catch up.",
  "Give us the outcome; we’ll choreograph the clicks.",
  "Good demos don’t tour. They reveal.",
  "Let’s make the useful parts impossible to miss.",
  "A clean demo is product truth with good timing.",
  "Show the work, skip the wandering.",
  "Let’s put the “why” between the clicks.",
  "Start with the customer win; the route comes next.",
  "Your workflow, edited for attention.",
  "Make every click move the story.",
  "Let’s turn the product path into a proof path.",
] as const;

export function getDailyAgentHeadline(date = new Date()) {
  const localDay = Math.floor(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()) / 86_400_000);
  return dailyAgentHeadlines[localDay % dailyAgentHeadlines.length] ?? dailyAgentHeadlines[0];
}

export function millisecondsUntilNextLocalDay(date = new Date()) {
  const nextDay = new Date(date.getFullYear(), date.getMonth(), date.getDate() + 1);
  return Math.max(1, nextDay.getTime() - date.getTime());
}

