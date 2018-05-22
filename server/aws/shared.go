package aws

import (
	"os"

	"log"

	"errors"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/gin-gonic/gin"
)

const (
	wrongAPIUsageError = "Ungültiger API-Aufruf: Die Argumente stimmen nicht mit der definition überein. Bitte erstelle eine Ticket"
	genericAwsAPIError = "Fehler beim Aufruf der AWS API. Bitte erstelle ein Ticket"
)

const (
	accountProd    = "prod"
	accountNonProd = "nonprod"
)

const (
	stageDev  = "dev"
	stageTest = "test"
	stageInt  = "int"
	stageProd = "prod"
)

const (
	bucketReadPolicy  = "-BucketReadPolicy"
	bucketWritePolicy = "-BucketWritePolicy"
)

func RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/aws/s3", listS3BucketsHandler)
	r.POST("/aws/s3", newS3BucketHandler)
	r.POST("/aws/s3/:bucketname/user", newS3UserHandler)

	r.GET("/aws/ec2", listEC2InstancesHandler)
	r.DELETE("/aws/snapshots/:account/:snapshotid", deleteEC2InstanceSnapshotHandler)
	r.POST("/aws/snapshots", createEC2InstanceSnapshotHandler)
	r.POST("/aws/ec2/:instanceid/:state", setEC2InstanceStateHandler)
}

func GetEC2Client(stage string) (*ec2.EC2, error) {
	account, err := getAccountForStage(stage)
	if err != nil {
		return nil, err
	}

	sess, err := getAwsSession(account)
	if err != nil {
		return nil, err
	}
	return ec2.New(sess), nil
}

func GetEC2ClientForAccount(account string) (*ec2.EC2, error) {
	var stage string
	if account == accountProd {
		stage = stageProd
	} else {
		stage = stageDev
	}

	svc, err := GetEC2Client(stage)
	if err != nil {
		log.Println("Error getting EC2 client: " + err.Error())
		return nil, err
	}
	return svc, nil
}

func GetS3Client(stage string) (*s3.S3, error) {
	account, err := getAccountForStage(stage)
	if err != nil {
		return nil, err
	}

	sess, err := getAwsSession(account)
	if err != nil {
		return nil, err
	}
	return s3.New(sess), nil
}

func GetIAMClient(stage string) (*iam.IAM, error) {
	account, err := getAccountForStage(stage)
	if err != nil {
		return nil, err
	}

	sess, err := getAwsSession(account)
	if err != nil {
		return nil, err
	}
	return iam.New(sess), nil
}

func getAwsSession(account string) (*session.Session, error) {
	// Validate necessary env variables
	region := os.Getenv("AWS_REGION")
	if len(region) == 0 {
		log.Fatal("Env variable 'AWS_REGION' must be specified")
	}
	bucketPrefix := os.Getenv("AWS_S3_BUCKET_PREFIX")
	if len(bucketPrefix) == 0 {
		log.Fatal("Env variable 'AWS_S3_BUCKET_PREFIX' must be specified")
	}

	// Create AWS session based on account
	var accessKeyID string
	var accessSecret string

	switch account {
	case accountProd:
		accessKeyID = os.Getenv("AWS_PROD_ACCESS_KEY_ID")
		accessSecret = os.Getenv("AWS_PROD_SECRET_ACCESS_KEY")
	case accountNonProd:
		accessKeyID = os.Getenv("AWS_NONPROD_ACCESS_KEY_ID")
		accessSecret = os.Getenv("AWS_NONPROD_SECRET_ACCESS_KEY")
	default:
		log.Println("Invalid account: " + account)
	}

	sess, err := session.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentials(accessKeyID, accessSecret, ""),
		Region:      aws.String(region)},
	)

	if err != nil {
		log.Println("Error creating aws session: ", err.Error())
		return nil, errors.New(genericAwsAPIError)
	}

	return sess, nil
}

// getAccountForStage remapps the stage string form the UI to
// the technical AWS account
// dev, test, int = NONPROD
// prod = PROD
func getAccountForStage(stage string) (string, error) {
	switch stage {
	case stageDev, stageTest, stageInt:
		return accountNonProd, nil
	case stageProd:
		return accountProd, nil
	default:
		log.Println("Could not map to account, invalid stage: " + stage)
		return "", errors.New(wrongAPIUsageError)
	}
}
