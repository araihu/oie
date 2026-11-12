CREATE INDEX IF NOT EXISTS reaction_command_message ON message_reaction_commands(message_id);
CREATE INDEX IF NOT EXISTS reaction_observation_command ON message_reaction_observations(command_id) WHERE command_id<>'';
PRAGMA user_version=24;
