import type {
  PluginProfileResponse,
  SkillProfileResponse,
} from "./types";

function arrayOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

/**
 * Contains pre-fix profile payloads that encoded absent collections as null.
 * The backend now emits arrays, but cached responses and mixed-version local
 * appliances can still cross this boundary during an upgrade.
 */
export function normalizeSkillProfile(profile: SkillProfileResponse): SkillProfileResponse {
  return {
    ...profile,
    assertions: arrayOrEmpty(profile.assertions),
    sources: arrayOrEmpty(profile.sources),
    file_tree: arrayOrEmpty(profile.file_tree),
  };
}

export function normalizePluginProfile(profile: PluginProfileResponse): PluginProfileResponse {
  return {
    ...profile,
    children: arrayOrEmpty(profile.children),
    versions: arrayOrEmpty(profile.versions),
    assertions: arrayOrEmpty(profile.assertions),
    sources: arrayOrEmpty(profile.sources),
  };
}
