# Setting up Aurora Postgres for out EKS cluster

If Aurora is already set up for your AWS account and EKS cluster, then skip down to the the instructions at "Creating a user for testing."

These instructions will be a mix of using the AWS console wizards along with a shell script to permit VPC routes from the EKS cluster to the Aurora instance.

The Aurora instance is created in its own VPC, since we expect it to potentially outlive our EKS test clusters. Since the EKS clusters do not have permanent state, we can reconfigure them with no downtime, unlike the database.

## Create Aurora Postgres Cluster with RDS Wizard

We use the RDS wizard to create the instance with its own VPC, and afterwards we rename the VPC to facilitate configuration and trouble-shooting.

At the top of the RDS console, click Create Database.
Use the following settings:
1. Select: Standard Create
1. Engine Options: Amazon Aurora with PostgreSQL
1. Select: Provisioned
1. Engine version: 12.4 (or latest)
1. Templates: dev/test
1. DB cluster id: zq-test-aurora
1. Master Username: postgres
1. Password: In some tools you have to type the password.
`openssl rand -base64 12 | awk '{print tolower($0)}'`
will generate a password that is easy to type.
1. Instance size: bustable class, t3.medium
1. Do not create relpica
1. VPC: create new VPC
1. Subnet Group: create new subnet group
1. Public Access: No
1. VPC Security Group: Create new, name zq-test-aurora/SecurityGroup
1. AZ: no pref
1. Database Auth: Password

It may take a half hour or more to create the RDS instance and VPCs.

After it is done, name the follwing objects that were generated by the wizard.
Find the VPC for the new RDS instance (which has  blank name) and rename it to:
`zq-test-aurora/VPC`
Navigate to the Main Route Table for the VPC, and rename it to:
`zq-test-aurora/MainRouteTable`
This allows the peering script below to find the right ids.

## Set up VPC peering

AFAICT, some of these steps to be done with imperative commands, so we will use a shell script rather than CloudFormation or Terraform. Run the script `k8s/peering.sh`, passing in the name of the EKS cluster and the RDS instance. Before running the script, read over it, since you will eventually need to understand everything it is doing. Run it first as a dry run.

Example, parameter order is important:
`./k8s/peering.sh eksctl-zq-test-cluster zq-test-aurora --dry-run`

Examine the output of the dry run carefully to make sure all the ids and CIDRs were found before running without the --dry-run.

Save the output! When you run without --dry-run, if there are any errors, it probably did not work correctly, but you may be able to fix things up by hand in the AWS console. You may need to eliminate blackhole routes in the EKS cluster public route table, and retry the one command that failed.

You can only run this particular script once for a given EKS cluster, because the route tables for multiple peering connections will conflict. If you need to run it again, delete the old peering connection, and remove the routes for 172.30.0.0/16 and 192.168.0.0/16 from the RDS MainRouteTable. Also remove any blackhole routes the the EKS public route table.

Note: I derived this script from an example I found:
https://dev.to/hayderimran7/create-a-simple-vpc-peer-between-kubernetes-and-rds-postgres-lhn
I liked this example because using it makes it unlikely that one of the AWS ids will be the wrong one, which is a easy mistake to make since the ids are found within lists of similar objects. (Specifying the wrong id is a hard mistake to touble-shoot!) I had to make a few changes, including adding a command to enable DNS propagation that I learned about at:
http://fruzenshtein.com/eks-kubernetes-aws-connect-rds/

## Test it from the EKS cluster
Start a bash session from a container:
```
kubectl run -i --tty --rm myshell --image=alpine -- sh
```
Then use psql to connect to the RDS instance with:
```
apk update
apk add postgresql
psql  -U postgres -h <RDS host>
```
You will need to type the password.

## Trouble-shooting

The peering connection created should have two route tables, one for each VPC. If it does not, something likely went wrong when the script was running.

The security group for the RDS VPC may need to be updated with the AWS console. It needs an inbound route for port 5432 with CIDR 192.168.0.0/16.

## Public accessiblity
I do not think we need a publicly accesible DB, but that could change. If you run the wizard and specify public accessiblity, we will need to do other things the be able to connect. See the trouble-shooting guide at: 
https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/CHAP_Troubleshooting.html#CHAP_Troubleshooting.Connecting
You will have to do the step described here:
"Internet gateway – For a DB instance to be publicly accessible, the subnets in its DB subnet group must have an internet gateway."  
"On the Route Table tab, verify that there is a route with 0.0.0.0/0 as the destination and the internet gateway for your VPC as the target."

## Creating a user for testing
To create users for testing, we will use kubectl, as above, to open an admin session with psql where we create a user and grant permissions. We use kubectl because we do currently do not allow connections to the database from outside the EKS cluster.
```
kubectl run -i --tty --rm myshell --image=alpine -- sh
# apk update
# apk add postgresql
# psql -U postgres -h <RDS host>
```
And use the following psql commands to create the user and a database:
```
CREATE USER theusername WITH ENCRYPTED PASSWORD 'thepassword';
ALTER USER theusername CREATEDB;
SET ROLE theusername;
CREATE DATABASE theusername;
```
The database name must be globally unique for this, so the database cannot be 'zqd'. By convention we create individual test databases in the Aurora cluster for each user, where the database name is the same as the username.

You can generate a password with something similar to:
```
openssl rand -base64 12 | awk '{print tolower($0)}'
```
In the future we would like to automate this, but given that this process will only need to be done once for each developer, it may not be worth it at this point.

## Configuring secrets for Aurora
After you have created a Postgres user for the aurora instance, create the K8s secrets for that user in correct namespace with the following command:
```
kubectl create secret generic aurora \
  --from-literal="postgresql-password=THEPASSWORD"
```
To make sure the secret looks right, try:
```
kubectl get secret aurora --template={{.data.postgresql-password}} | base64 --decode
```
Your zqd sessions will connect to the db using this username (not the master username) and the session will have permission to create a database and perform the migrations.

## Helm Deploy with Aurora
In order to deploy zqd with Aurora access, you must set an additional environment variable:
```
ZQD_AURORA_USER=theusername
```
And use this alternate Makefile target to deploy:
```
make helm-install-with-aurora
```
This target includes are the values overrides that are needed to deploy a zqd cluster which uses the zq-test-aurora instance (rather than a postgres container in K8s).