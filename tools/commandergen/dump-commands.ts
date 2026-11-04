// Dumps the TypeScript CLI's Commander tree as JSON, for commandergen.
//
//   bun tools/commandergen/dump-commands.ts <typescript-sdk>/packages/cli/src/cli.ts \
//     > internal/cli/testdata/typescript-commands.json
const { createCli } = await import(process.argv[2]);

type Node = {
  path: string;
  description: string;
  arguments: { name: string; required: boolean; variadic: boolean }[];
  options: {
    flags: string;
    long: string | null;
    short: string | null;
    mandatory: boolean;
    takesValue: boolean;
    optionalValue: boolean;
    negate: boolean;
    hidden: boolean;
    choices: string[] | null;
    parser: string | null;
    description: string;
    defaultValue: unknown;
  }[];
  hasSubcommands: boolean;
  hasAction: boolean;
};

const out: Node[] = [];
// biome-ignore lint/suspicious/noExplicitAny: Commander's internals are not typed.
function walk(cmd: any, prefix: string[]) {
  const path = [...prefix, cmd.name()].slice(1).join(' ');
  out.push({
    path,
    description: cmd.description(),
    // biome-ignore lint/suspicious/noExplicitAny: Commander's internals are not typed.
    arguments: cmd.registeredArguments.map((a: any) => ({
      name: a.name(),
      required: a.required,
      variadic: a.variadic,
    })),
    // biome-ignore lint/suspicious/noExplicitAny: Commander's internals are not typed.
    options: cmd.options.map((o: any) => ({
      flags: o.flags,
      long: o.long ?? null,
      short: o.short ?? null,
      mandatory: !!o.mandatory,
      takesValue: !!(o.required || o.optional),
      optionalValue: !!o.optional,
      negate: !!o.negate,
      hidden: !!o.hidden,
      choices: o.argChoices ?? null,
      // The name of an argParser function; choices install an unnamed one.
      parser: o.argChoices ? null : o.parseArg?.name || null,
      description: o.description,
      defaultValue: o.defaultValue ?? null,
    })),
    hasSubcommands: cmd.commands.length > 0,
    hasAction: !!cmd._actionHandler,
  });
  for (const sub of cmd.commands) walk(sub, [...prefix, cmd.name()]);
}
walk(createCli('0.0.0'), []);
console.log(JSON.stringify(out, null, 2));
