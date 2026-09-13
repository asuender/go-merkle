# go-merkle

Single-leader file replication based on Merkle trees. Implemented in Go.

## Proposed implementation

I recently saw a video by Ben Dicken about [Merkle trees](https://www.youtube.com/watch?v=86Elcm_6X_Y) (great video btw!) and thought: hm, that's cool, let's build something around them! Eventually, I came up with the following:

Imagine a network of multiple clients connected together, with the goal of replicating files stored in one of those clients, the leader. All the other nodes are followers that subscribe to any changes and sync them. Followers are meant to be started in empty directories (for now), whereas the leader picks up the files in the directory it's spawned in. Furthermore, client may live on the same machine or somewhere else.

I imagine a basic flow as follows:

1. Leader starts up
2. Followers connect to the leader
3. Followers individually ask for the initial tree
4. Leader responds by sending the tree structure (without actual file contents)
5. After receiving the tree, followers request individual file contents by their hash values
6. Followers repeatedly poll the leader for updates
7. If any updates occur, followers performs a diff / merge algorithm to determine which contents have to be (re-)fetched
8. Followers then request updated file contents from the leader

## License

[MIT](https://choosealicense.com/licenses/mit/)
