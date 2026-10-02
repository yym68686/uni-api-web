import { aliasMappings } from "./ModelAliases";
import type { ModelAlias } from "./ModelAliases";

export function readCreationModels(value: unknown) {
  if (!Array.isArray(value)) throw Error("模型配置必须是列表。");
  const originals: string[] = [],
    aliases: ModelAlias[] = [];
  for (const item of value) {
    if (typeof item === "string") originals.push(item);
    else if (
      item &&
      typeof item === "object" &&
      !Array.isArray(item) &&
      Object.keys(item).length
    ) {
      for (const [upstream, name] of Object.entries(item)) {
        if (typeof name !== "string") throw Error("模型映射名称必须是文字。");
        if (name === upstream) originals.push(upstream);
        else aliases.push({ upstream, public: name });
      }
    } else throw Error("模型配置需为模型名称或重命名映射。");
  }
  writeCreationModels(originals, aliases);
  return { originals, aliases };
}

export function writeCreationModels(
  originals: string[],
  aliases: ModelAlias[],
) {
  if (
    originals.some((m) => !m.trim() || /[\r\n\t]/.test(m)) ||
    aliases.some((a) => !a.upstream.trim() || /[\r\n\t]/.test(a.upstream))
  )
    throw Error("模型名称不能为空或包含换行。");
  if (new Set(originals).size !== originals.length)
    throw Error("模型名称重复。");
  const { error } = aliasMappings(aliases, originals);
  if (error) throw Error(error);
  return [
    ...originals,
    ...aliases.map((a) => ({ [a.upstream]: a.public.trim() })),
  ];
}
