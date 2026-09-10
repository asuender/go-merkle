# go-merkle

Single-leader file replication based on Merkle trees. Implemented in Go.

I recently saw a video by Ben Dicken about [Merkle trees](https://www.youtube.com/watch?v=86Elcm_6X_Y) (great video btw!) and thought: hm, that's cool, let's build something around them! Eventually, I came up with the following idea:

Imagine network of clients connected together where one of them is the leader and the others are followers, in a sense that there is one place where the original data is stored / modified and multiple nodes just subscribe to those changes for syncing. Those other clients could live on the same machine (perhaps in another folder) or somewhere else.

A possible flow could be as follows: leader starts up, followers connect to it. Followers ask for initial tree, client sends it. Then, follower asks for individual contents by their hash values as "id". Follower repeatedly poll leader, leader sends full tree back. Followers then perform a merge / diff algorithm between the old and the new tree. Ff contents have changed or added (because hash values changed), followers ask for updated contents for each of those files.
