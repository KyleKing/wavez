"""A tiny fixture for symbol extraction tests."""


class Greeter:
    """Says hello to someone by name."""

    def greet(self, name):
        """Return a greeting for name."""
        return self.prefix + name


def new_greeter(prefix):
    """Build a Greeter with the given prefix."""
    return Greeter(prefix)
