We need to integrate web socket for updating information regarding offer and sending of proof. 
Currently we are using pooling to fetch all the offers - we should probably use websocket here 
When sending and uploading file we are not refreshing the other hand ( for example when user send proof of payment 
agent need to manually refresh the page to see that)

1. We need to make a list of all places where websocket update could be pertinent 
2. We need to come up with a implementation plan for both frontend and backend