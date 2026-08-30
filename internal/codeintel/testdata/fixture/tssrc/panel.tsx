/** Renders a greeting into a panel. */
export function GreetPanel(props: { name: string }) {
	return <div className="panel">{props.name}</div>;
}
