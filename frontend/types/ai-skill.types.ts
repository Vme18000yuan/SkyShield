export interface AISkillInput { displayName: string; description: string; instructions: string; enabled: boolean; agents: string[] }
export interface AISkill extends AISkillInput { id: string; name: string; builtin?: boolean; createdAt: string; updatedAt: string }
