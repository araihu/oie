CREATE INDEX reaction_command_message ON message_reaction_commands(message_id);
CREATE INDEX reaction_observation_command ON message_reaction_observations(command_id) WHERE command_id<>'';
PRAGMA user_version=21;
