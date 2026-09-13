// Central configuration, read from environment variables with sane defaults.
// Real secrets belong in a git-ignored .env file (see .env.example).

const toInt = (value, fallback) => {
  const parsed = parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : fallback;
};

export const config = {
  port: toInt(process.env.PORT, 3000),

  // Only the admin can create rooms (for now).
  adminUser: process.env.ADMIN_USER || "admin",
  adminPassword: process.env.ADMIN_PASSWORD || "change-me",

  // A room with nobody broadcasting for this long gets closed.
  roomTtlMinutes: toInt(process.env.ROOM_TTL_MINUTES, 10),

  // Broadcaster + viewers. P2P mesh stays cheap only with small rooms.
  maxParticipants: toInt(process.env.MAX_PARTICIPANTS, 3),
};
