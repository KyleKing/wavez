/** Says hello to someone by name. */
export class Greeter {
	prefix: string;

	/** Return a greeting for name. */
	greet(name: string): string {
		return this.prefix + name;
	}
}

/** Build a Greeter with the given prefix. */
export function newGreeter(prefix: string): Greeter {
	return new Greeter(prefix);
}

/** The shape a greeting source has to satisfy. */
export interface GreetSource {
	prefix: string;
}

/** A name a greeter will accept. */
export type GreetName = string;
