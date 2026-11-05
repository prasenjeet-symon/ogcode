"""The category tree shared by rules, facets and reporting.

Categories are stored as a nested mapping because the tree is small and the
nesting is the point: a facet filter on `hardware` has to reach `tools`
underneath it without a second lookup table to maintain.
"""

TREE = {
    "hardware": {"tools": {}, "fasteners": {}},
    "living": {"home": {}, "garden": {}},
    "apparel": {"outerwear": {}, "footwear": {}},
    "clearance": {},
    "digital": {},
}


def flatten(tree=None, parent=None, into=None):
    """A flat ``category -> parent`` map, parent ``None`` at the roots."""
    tree = TREE if tree is None else tree
    into = {} if into is None else into
    for name, children in tree.items():
        into[name] = parent
        flatten(children, name, into)
    return into


def path_of(category: str) -> tuple:
    """The chain from the root down to ``category``, root first."""
    parents = flatten()
    if category not in parents:
        return ()
    chain = [category]
    while parents[chain[-1]] is not None:
        chain.append(parents[chain[-1]])
    return tuple(reversed(chain))


def is_ancestor(ancestor: str, category: str) -> bool:
    """True when ``category`` sits anywhere under ``ancestor``."""
    return ancestor in path_of(category)


def leaves(tree=None) -> list:
    """Every category with no children, sorted."""
    tree = TREE if tree is None else tree
    found = []
    for name, children in tree.items():
        if children:
            found.extend(leaves(children))
        else:
            found.append(name)
    return sorted(found)


def depth(category: str) -> int:
    """How deep the category sits: roots are 1, so an unknown key is 0."""
    return len(path_of(category))
